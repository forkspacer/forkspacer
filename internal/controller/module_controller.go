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
	"context"
	"errors"
	"fmt"
	"time"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	"github.com/forkspacer/forkspacer/pkg/manager"
	managerBase "github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
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
		r.Recorder.Event(module, "Warning", "InternalError",
			fmt.Sprintf("encountered unknown module phase: %s", module.Status.Phase),
		)
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

		if err := r.setPhase(
			ctx, module, batchv1.ModulePhaseFailed,
			utils.ToPtr("Module installation failed. Check events for more information."),
		); err != nil {
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
	if module.Status.Phase == batchv1.ModulePhaseReady && module.Spec.Hibernated {
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
	if module.Status.Phase == batchv1.ModulePhaseSleeped && !module.Spec.Hibernated {
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

func (r *ModuleReconciler) setPhase(
	ctx context.Context,
	module *batchv1.Module,
	phase batchv1.ModulePhaseType,
	message *string,
) error {
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

func (r *ModuleReconciler) newManager(
	ctx context.Context,
	module *batchv1.Module,
	workspaceConnection *batchv1.WorkspaceConnection,
	moduleData []byte,
	metaData managerBase.MetaData,
	configMap map[string]any,
) (managerBase.IManager, error) {
	log := logf.FromContext(ctx)

	var iManager managerBase.IManager

	err := resources.HandleResource(moduleData, &configMap,
		func(helmModule resources.HelmModule) error {
			var releaseName string
			metaDataReleaseName, ok := metaData[managerCons.HelmMetaDataKeys.ReleaseName]
			if ok {
				releaseName = metaDataReleaseName.(string)
			} else {
				releaseName = newHelmReleaseNameFromModule(*module)
				metaData[managerCons.HelmMetaDataKeys.ReleaseName] = releaseName
			}

			err := helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
			if err != nil {
				return fmt.Errorf("failed to render Helm module spec: %v", err)
			}

			helmService, err := r.newHelmService(ctx, workspaceConnection)
			if err != nil {
				return err
			}

			iManager, err = manager.NewModuleHelmManager(&helmModule, helmService, releaseName, logf.FromContext(ctx))
			if err != nil {
				return fmt.Errorf("failed to install Helm module: %w", err)
			}
			return nil
		},
		func(customModule resources.CustomModule) error {
			apiConfig, err := r.newAPIConfig(ctx, workspaceConnection)
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes config: %w", err)
			}

			iManager, err = manager.NewModuleCustomManager(ctx, r.Client, apiConfig, &customModule, configMap, metaData)
			if err != nil {
				return fmt.Errorf("failed to install Custom module: %w", err)
			}
			return nil
		},
	)

	patch := client.MergeFrom(module.DeepCopy())
	utils.UpdateMap(&module.Annotations, map[string]string{
		kubernetesCons.ModuleAnnotationKeys.ManagerData: metaData.String(),
	})
	if err := r.Patch(ctx, module, patch); err != nil {
		log.Error(err, "failed to patch module with manager data", "module", module.Name, "namespace", module.Namespace)
	}

	return iManager, err
}

func (r *ModuleReconciler) newHelmService(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
) (*services.HelmService, error) {
	return NewHelmService(ctx, workspaceConn, r.Client)
}

func (r *ModuleReconciler) newAPIConfig(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
) (*clientcmdapi.Config, error) {
	return NewAPIConfig(ctx, workspaceConn, r.Client)
}

func (r *ModuleReconciler) getWorkspace(
	ctx context.Context,
	workspaceRef *batchv1.ModuleWorkspaceReference,
) (*batchv1.Workspace, error) {
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
