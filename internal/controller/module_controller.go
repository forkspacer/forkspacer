/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	managerBase "github.com/forkspacer/forkspacer/pkg/manager/base"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/manager"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	"github.com/forkspacer/forkspacer/pkg/types"
	"github.com/forkspacer/forkspacer/pkg/utils"
)

var moduleFinalizers = struct {
	Delete string
}{
	Delete: "delete",
}

// ModuleReconciler reconciles a Module object
type ModuleReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	module := &batchv1.Module{}
	if err := r.Get(ctx, req.NamespacedName, module); err != nil {
		if k8sErrors.IsNotFound(err) {
			log.Info("Module resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Module")
		return ctrl.Result{}, err
	}

	if module.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, module)
	}

	switch module.Status.Phase {
	case "":
		return r.handleEmptyPhase(ctx, module)

	case batchv1.ModulePhaseReady, batchv1.ModulePhaseSleeped:
		return r.handleHibernation(ctx, module)

	case batchv1.ModulePhaseFailed:
		return ctrl.Result{}, nil

	case batchv1.ModulePhaseInstalling, batchv1.ModulePhaseUninstalling,
		batchv1.ModulePhaseSleeping, batchv1.ModulePhaseResuming:
		return ctrl.Result{RequeueAfter: time.Second * 2}, nil

	default:
		log.Error(errors.New("unknown module phase"), "encountered unknown module phase", "phase", module.Status.Phase)
		r.Recorder.Event(module, "Warning", "InternalError", fmt.Sprintf("encountered unknown module phase: %s", module.Status.Phase))
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_ = mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&batchv1.Module{},
		"spec.workspace.name",
		func(obj client.Object) []string {
			resource := obj.(*batchv1.Module)
			return []string{resource.Spec.Workspace.Name}
		},
	)
	_ = mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&batchv1.Module{},
		"spec.workspace.namespace",
		func(obj client.Object) []string {
			resource := obj.(*batchv1.Module)
			return []string{resource.Spec.Workspace.Namespace}
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.Module{}).
		Named("module").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		Complete(r)
}

func (r *ModuleReconciler) handleDeletion(ctx context.Context, module *batchv1.Module) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	switch module.Status.Phase {
	case batchv1.ModulePhaseReady, batchv1.ModulePhaseFailed, batchv1.ModulePhaseSleeped:
	default:
		return ctrl.Result{RequeueAfter: time.Second * 2}, nil
	}

	if err := r.setPhase(ctx, module, batchv1.ModulePhaseUninstalling, nil); err != nil {
		return ctrl.Result{}, err
	}

	modulePhase := module.Status.Phase
	if err := r.uninstallModule(ctx, module); err != nil {
		log.Error(err, "module uninstallation failed")
		if modulePhase != batchv1.ModulePhaseFailed {
			r.Recorder.Event(module, "Warning", "UninstallError", fmt.Sprintf("Module uninstallation failed: %v", err))
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(module), module); err != nil {
				return err
			}

			if !controllerutil.ContainsFinalizer(module, moduleFinalizers.Delete) {
				return nil
			}

			controllerutil.RemoveFinalizer(module, moduleFinalizers.Delete)
			return r.Update(ctx, module)
		},
	)
}

func (r *ModuleReconciler) handleEmptyPhase(ctx context.Context, module *batchv1.Module) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	err := retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(module), module); err != nil {
				return err
			}

			if controllerutil.ContainsFinalizer(module, moduleFinalizers.Delete) {
				return nil
			}

			controllerutil.AddFinalizer(module, moduleFinalizers.Delete)
			return r.Update(ctx, module)
		},
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.setPhase(ctx, module, batchv1.ModulePhaseInstalling, nil); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.installModule(ctx, module); err != nil {
		log.Error(err, "module installation failed")
		r.Recorder.Event(module, "Warning", "InstallError", fmt.Sprintf("Module installation failed: %v", err))

		if err := r.setPhase(ctx, module, batchv1.ModulePhaseFailed, utils.ToPtr("Module installation failed. Check events for more information.")); err != nil {
			log.Error(err, "failed to update module status to Failed after installation error")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if err := r.setPhase(ctx, module, batchv1.ModulePhaseReady, utils.ToPtr("Module installed successfully")); err != nil {
		log.Error(err, "failed to update module status to Ready after successful installation")
		return ctrl.Result{}, err
	}

	return r.handleHibernation(ctx, module)
}

func (r *ModuleReconciler) handleHibernation(ctx context.Context, module *batchv1.Module) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Sleep
	if module.Status.Phase == batchv1.ModulePhaseReady && utils.NotNilAndZero(module.Spec.Hibernated) {
		if err := r.setPhase(ctx, module, batchv1.ModulePhaseSleeping, nil); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.sleepModule(ctx, module); err != nil {
			log.Error(err, "module sleep failed")
			r.Recorder.Event(module, "Warning", "SleepError", fmt.Sprintf("Module sleep failed: %v", err))
			return ctrl.Result{}, err
		}

		if err := r.setPhase(ctx, module, batchv1.ModulePhaseSleeped, nil); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resume
	if module.Status.Phase == batchv1.ModulePhaseSleeped && utils.NotNilAndNot(module.Spec.Hibernated, true) {
		if err := r.setPhase(ctx, module, batchv1.ModulePhaseResuming, nil); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.resumeModule(ctx, module); err != nil {
			log.Error(err, "module resume failed")
			r.Recorder.Event(module, "Warning", "ResumeError", fmt.Sprintf("Module resume failed: %v", err))
			return ctrl.Result{}, err
		}

		if err := r.setPhase(ctx, module, batchv1.ModulePhaseReady, nil); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: time.Second * 2}, nil
}

func (r *ModuleReconciler) setPhase(ctx context.Context, module *batchv1.Module, phase batchv1.ModulePhaseType, message *string) error {
	return retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(module), module); err != nil {
				return err
			}

			module.Status = batchv1.ModuleStatus{
				Phase:        phase,
				LastActivity: &metav1.Time{Time: time.Now()},
				Message:      message,
			}

			return r.Status().Update(ctx, module)
		},
	)
}

func (r *ModuleReconciler) installModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %v", err)
	}

	if !workspace.Status.Ready {
		return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
	}

	if workspace.Status.Phase != batchv1.WorkspacePhaseReady {
		return fmt.Errorf(
			"workspace not in running phase: workspace %s/%s is not in '%s' phase, current phase is %s",
			workspace.Namespace, workspace.Name, batchv1.WorkspacePhaseReady, workspace.Status.Phase,
		)
	}

	moduleReader, err := r.readModuleLocation(module.Spec.Source)
	if err != nil {
		return fmt.Errorf("failed to read module location: %v", err)
	}

	moduleData, err := io.ReadAll(moduleReader)
	if err != nil {
		return fmt.Errorf("failed to read module data: %v", err)
	}

	annotations := map[string]string{
		types.ModuleAnnotationKeys.Resource: string(moduleData),
	}

	metaData := make(managerBase.MetaData)

	var configMap map[string]any
	if module.Spec.Config != nil && module.Spec.Config.Raw != nil {
		if err := json.Unmarshal(module.Spec.Config.Raw, &configMap); err != nil {
			return fmt.Errorf("failed to unmarshal module config: %v", err)
		}

		annotations[types.ModuleAnnotationKeys.BaseModuleConfig] = string(module.Spec.Config.Raw)
	}

	installErr := resources.HandleResource(moduleData, &configMap,
		func(helmModule resources.HelmModule) error {
			releaseName := getHelmReleaseNameFromModule(*module)
			metaData[manager.HelmMetaDataKeys.ReleaseName] = releaseName

			err = helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
			if err != nil {
				return fmt.Errorf("failed to render Helm module spec: %v", err)
			}

			helmService, err := r.newHelmService(ctx, workspace.Spec.Connection)
			if err != nil {
				return err
			}

			helmManager, err := manager.NewModuleHelmManager(&helmModule, helmService, releaseName, logf.FromContext(ctx))
			if err != nil {
				return fmt.Errorf("failed to install Helm module: %w", err)
			}

			err = helmManager.Install(ctx, metaData)
			if err != nil {
				return fmt.Errorf("failed to install Helm module: %v", err)
			}
			return nil
		},
		func(customModule resources.CustomModule) error {
			kubernetesConfig, err := r.newKubernetesConfig(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes config: %w", err)
			}

			customManager, err := manager.NewModuleCustomManager(ctx, r.Client, kubernetesConfig, &customModule, configMap, metaData)
			if err != nil {
				return fmt.Errorf("failed to install Custom module: %w", err)
			}

			err = customManager.Install(ctx, metaData)
			if err != nil {
				return fmt.Errorf("failed to install Custom module: %v", err)
			}
			return nil
		},
	)

	patch := client.MergeFrom(module.DeepCopy())
	annotations[types.ModuleAnnotationKeys.ManagerData] = metaData.String()
	utils.UpdateMap(&module.Annotations, annotations)
	if err = r.Patch(ctx, module, patch); err != nil {
		log.Error(err, "failed to patch module with install annotations", "module", module.Name, "namespace", module.Namespace)
	}

	return installErr
}

func (r *ModuleReconciler) uninstallModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	if !workspace.Status.Ready {
		return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
	}

	switch workspace.Status.Phase {
	case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, batchv1.WorkspacePhaseFailed:
	default:
		return fmt.Errorf(
			"workspace is not in a suitable phase for uninstallation: expected one of ['%s', '%s', '%s'], but got '%s'",
			batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, batchv1.WorkspacePhaseFailed, workspace.Status.Phase,
		)
	}

	utils.InitMap(&module.Annotations)

	resourceAnnotation := module.Annotations[types.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return fmt.Errorf("resource definition not found in module annotations for %s/%s", module.Namespace, module.Name)
	}
	moduleData := []byte(resourceAnnotation)

	managerData, ok := module.Annotations[types.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with uninstall even if metadata parsing fails, as it might not be critical for uninstall
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[types.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	return resources.HandleResource(moduleData, nil,
		func(helmModule resources.HelmModule) error {
			releaseName, ok := metaData[manager.HelmMetaDataKeys.ReleaseName].(string)
			if !ok || releaseName == "" {
				return fmt.Errorf(
					"helm release name not found in module metadata for module %s/%s. Unable to uninstall",
					module.Namespace, module.Name,
				)
			}

			err = helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
			if err != nil {
				return fmt.Errorf("failed to render Helm module spec: %v", err)
			}

			helmService, err := r.newHelmService(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Helm service for workspace %s/%s: %v", workspace.Namespace, workspace.Name, err)
			}

			manager, err := manager.NewModuleHelmManager(&helmModule, helmService, releaseName, logf.FromContext(ctx))
			if err != nil {
				return fmt.Errorf("failed to create Helm manager for module %s/%s: %w", module.Namespace, module.Name, err)
			}

			if err = manager.Uninstall(ctx, metaData); err != nil {
				return fmt.Errorf("failed to uninstall Helm module %s/%s: %v", module.Namespace, module.Name, err)
			}

			return nil
		},
		func(customModule resources.CustomModule) error {
			kubernetesConfig, err := r.newKubernetesConfig(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes config: %w", err)
			}

			manager, err := manager.NewModuleCustomManager(ctx, r.Client, kubernetesConfig, &customModule, configMap, metaData)
			if err != nil {
				return fmt.Errorf("failed to uninstall Custom module: %w", err)
			}

			patch := client.MergeFrom(module.DeepCopy())
			module.Annotations[types.ModuleAnnotationKeys.ManagerData] = metaData.String()
			if err = r.Patch(ctx, module, patch); err != nil {
				log.Error(err, "failed to patch module with meta data annotations after 'NewModuleCustomManager'", "module", module.Name, "namespace", module.Namespace)
			}

			if err = manager.Uninstall(ctx, metaData); err != nil {
				return fmt.Errorf("failed to uninstall Custom module %s/%s: %v", module.Namespace, module.Name, err)
			}

			return nil
		},
	)
}

func (r *ModuleReconciler) sleepModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	if !workspace.Status.Ready {
		return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
	}

	switch workspace.Status.Phase {
	case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated:
	default:
		return fmt.Errorf(
			"workspace is not in a suitable phase for sleeping: expected one of ['%s', '%s'], but got '%s'",
			batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, workspace.Status.Phase,
		)
	}

	utils.InitMap(&module.Annotations)

	resourceAnnotation := module.Annotations[types.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return fmt.Errorf("resource definition not found in module annotations for %s/%s", module.Namespace, module.Name)
	}
	moduleData := []byte(resourceAnnotation)

	managerData, ok := module.Annotations[types.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with sleep even if metadata parsing fails, as it might not be critical for sleep
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[types.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	err = resources.HandleResource(moduleData, nil,
		func(helmModule resources.HelmModule) error {
			releaseName, ok := metaData[manager.HelmMetaDataKeys.ReleaseName].(string)
			if !ok || releaseName == "" {
				return fmt.Errorf(
					"helm release name not found in module metadata for module %s/%s",
					module.Namespace, module.Name,
				)
			}

			err = helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
			if err != nil {
				return fmt.Errorf("failed to render Helm module spec: %v", err)
			}

			helmService, err := r.newHelmService(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Helm service for workspace %s/%s: %v", workspace.Namespace, workspace.Name, err)
			}

			helmManager, err := manager.NewModuleHelmManager(&helmModule, helmService, releaseName, logf.FromContext(ctx))
			if err != nil {
				return fmt.Errorf("failed to create Helm manager for module %s/%s: %w", module.Namespace, module.Name, err)
			}

			if err = helmManager.Sleep(ctx, metaData); err != nil {
				return fmt.Errorf("failed to sleep Helm module %s/%s: %v", module.Namespace, module.Name, err)
			}

			return nil
		},
		func(customModule resources.CustomModule) error {
			kubernetesConfig, err := r.newKubernetesConfig(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes config: %w", err)
			}

			customManager, err := manager.NewModuleCustomManager(ctx, r.Client, kubernetesConfig, &customModule, configMap, metaData)
			if err != nil {
				return fmt.Errorf("failed to sleep Custom module: %w", err)
			}

			patch := client.MergeFrom(module.DeepCopy())
			module.Annotations[types.ModuleAnnotationKeys.ManagerData] = metaData.String()
			if err = r.Patch(ctx, module, patch); err != nil {
				log.Error(err, "failed to patch module with meta data annotations after 'NewModuleCustomManager'", "module", module.Name, "namespace", module.Namespace)
			}

			if err = customManager.Sleep(ctx, metaData); err != nil {
				return fmt.Errorf("failed to sleep Custom module %s/%s: %v", module.Namespace, module.Name, err)
			}

			return nil
		},
	)

	patch := client.MergeFrom(module.DeepCopy())
	module.Annotations[types.ModuleAnnotationKeys.ManagerData] = metaData.String()
	if err := r.Patch(ctx, module, patch); err != nil {
		log.Error(err, "failed to patch module with meta data annotations after sleep", "module", module.Name, "namespace", module.Namespace)
	}

	return err
}

func (r *ModuleReconciler) resumeModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	if !workspace.Status.Ready {
		return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
	}

	switch workspace.Status.Phase {
	case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated:
	default:
		return fmt.Errorf(
			"workspace is not in a suitable phase for sleeping: expected one of ['%s', '%s'], but got '%s'",
			batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, workspace.Status.Phase,
		)
	}

	utils.InitMap(&module.Annotations)

	resourceAnnotation := module.Annotations[types.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return fmt.Errorf("resource definition not found in module annotations for %s/%s", module.Namespace, module.Name)
	}
	moduleData := []byte(resourceAnnotation)

	managerData, ok := module.Annotations[types.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with resume even if metadata parsing fails, as it might not be critical for resume
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[types.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	err = resources.HandleResource(moduleData, nil,
		func(helmModule resources.HelmModule) error {
			releaseName, ok := metaData[manager.HelmMetaDataKeys.ReleaseName].(string)
			if !ok || releaseName == "" {
				return fmt.Errorf(
					"helm release name not found in module metadata for module %s/%s",
					module.Namespace, module.Name,
				)
			}

			err = helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
			if err != nil {
				return fmt.Errorf("failed to render Helm module spec: %v", err)
			}

			helmService, err := r.newHelmService(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Helm service for workspace %s/%s: %v", workspace.Namespace, workspace.Name, err)
			}

			manager, err := manager.NewModuleHelmManager(&helmModule, helmService, releaseName, logf.FromContext(ctx))
			if err != nil {
				return fmt.Errorf("failed to create Helm manager for module %s/%s: %w", module.Namespace, module.Name, err)
			}

			if err = manager.Resume(ctx, metaData); err != nil {
				return fmt.Errorf("failed to resume Helm module %s/%s: %v", module.Namespace, module.Name, err)
			}

			return nil
		},
		func(customModule resources.CustomModule) error {
			kubernetesConfig, err := r.newKubernetesConfig(ctx, workspace.Spec.Connection)
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes config: %w", err)
			}

			manager, err := manager.NewModuleCustomManager(ctx, r.Client, kubernetesConfig, &customModule, configMap, metaData)
			if err != nil {
				return fmt.Errorf("failed to resume Custom module: %w", err)
			}

			patch := client.MergeFrom(module.DeepCopy())
			module.Annotations[types.ModuleAnnotationKeys.ManagerData] = metaData.String()
			if err = r.Patch(ctx, module, patch); err != nil {
				log.Error(err, "failed to patch module with meta data annotations after 'NewModuleCustomManager'", "module", module.Name, "namespace", module.Namespace)
			}

			if err = manager.Resume(ctx, metaData); err != nil {
				return fmt.Errorf("failed to resume Custom module %s/%s: %v", module.Namespace, module.Name, err)
			}

			return nil
		},
	)

	patch := client.MergeFrom(module.DeepCopy())
	module.Annotations[types.ModuleAnnotationKeys.ManagerData] = metaData.String()
	if err := r.Patch(ctx, module, patch); err != nil {
		log.Error(err, "failed to patch module with meta data annotations after resume", "module", module.Name, "namespace", module.Namespace)
	}

	return err
}

func (r *ModuleReconciler) newHelmService(ctx context.Context, workspaceConn *batchv1.WorkspaceConnection) (*services.HelmService, error) {
	return NewHelmService(ctx, workspaceConn, r.Client)
}

func (r *ModuleReconciler) newKubernetesConfig(ctx context.Context, workspaceConn *batchv1.WorkspaceConnection) (*rest.Config, error) {
	return NewKubernetesConfig(ctx, workspaceConn, r.Client)
}

func (r *ModuleReconciler) getWorkspace(ctx context.Context, workspaceRef *batchv1.ModuleWorkspaceReference) (*batchv1.Workspace, error) {
	workspace := &batchv1.Workspace{}
	namespacedName := client.ObjectKey{
		Name:      workspaceRef.Name,
		Namespace: workspaceRef.Namespace,
	}

	if err := r.Get(ctx, namespacedName, workspace); err != nil {
		return nil, err
	}

	return workspace, nil
}

func (r *ModuleReconciler) readModuleLocation(moduleSource batchv1.ModuleSource) (io.Reader, error) {
	if moduleSource.Raw != nil {
		return bytes.NewReader(moduleSource.Raw.Raw), nil
	} else if moduleSource.HttpURL != nil {
		resp, err := http.Get(*moduleSource.HttpURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch module from HTTP URL %s: %w", *moduleSource.HttpURL, err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP request failed with status %d for URL %s", resp.StatusCode, *moduleSource.HttpURL)
		}
		return resp.Body, nil
	} else if moduleSource.Github != nil {
		return nil, errors.New("'github' module location type is not yet supported")
	} else {
		return nil, errors.New("exactly one of 'raw', 'httpURL', or 'github' must be specified")
	}
}
