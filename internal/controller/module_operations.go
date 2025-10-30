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
	"encoding/json"
	"fmt"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerBase "github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/utils"
)

// ErrWorkspaceNotReady indicates that the workspace exists but is not ready yet
// Controllers should requeue when this error is encountered
type ErrWorkspaceNotReady struct {
	WorkspaceName      string
	WorkspaceNamespace string
	Message            string
}

func (e *ErrWorkspaceNotReady) Error() string {
	return fmt.Sprintf("workspace %s/%s not ready: %s", e.WorkspaceNamespace, e.WorkspaceName, e.Message)
}

func (r *ModuleReconciler) installModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %v", err)
	}

	// If workspace is not ready yet, return a special error that tells the controller to requeue
	if !workspace.Status.Ready {
		return &ErrWorkspaceNotReady{
			WorkspaceName:      workspace.Name,
			WorkspaceNamespace: workspace.Namespace,
			Message:            "workspace exists but status.ready is false",
		}
	}

	if workspace.Status.Phase != batchv1.WorkspacePhaseReady {
		return &ErrWorkspaceNotReady{
			WorkspaceName:      workspace.Name,
			WorkspaceNamespace: workspace.Namespace,
			Message: fmt.Sprintf("workspace phase is %s, waiting for %s",
				workspace.Status.Phase, batchv1.WorkspacePhaseReady),
		}
	}

	if module.Spec.Helm != nil && module.Spec.Helm.ExistingRelease != nil {
		if err := r.adoptExistingHelmRelease(ctx, workspace, module); err != nil {
			return fmt.Errorf("failed to adopt existing Helm release: %w", err)
		}
		return nil
	}

	annotations := make(map[string]string)

	configMap := make(map[string]any)
	if module.Spec.Config != nil && module.Spec.Config.Raw != nil {
		if err := json.Unmarshal(module.Spec.Config.Raw, &configMap); err != nil {
			return fmt.Errorf("failed to unmarshal module config: %v", err)
		}
	}

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]
	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with uninstall even if metadata parsing fails, as it might not be critical for uninstall
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		metaData, configMap,
	); err != nil {
		return err
	} else {
		installErr := iManager.Install(ctx, metaData)

		annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData] = metaData.String()

		patch := client.MergeFrom(module.DeepCopy())
		utils.UpdateMap(&module.Annotations, annotations)
		if err = r.Patch(ctx, module, patch); err != nil {
			log.Error(err,
				"failed to patch module with install annotations",
				"module", module.Name, "namespace", module.Namespace,
			)
		}

		if installErr != nil {
			return fmt.Errorf("failed to install module: %w", installErr)
		}
		return nil
	}
}

func (r *ModuleReconciler) uninstallModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	// Skip uninstallation for adopted releases - just detach
	if module.Spec.Helm != nil && module.Spec.Helm.ExistingRelease != nil {
		log.Info("skipping uninstall for adopted Helm release - only detaching from module")
		return nil
	}

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		// If workspace doesn't exist (already deleted), skip uninstall
		// This prevents orphaned modules from getting stuck when workspace is deleted
		if k8sErrors.IsNotFound(err) {
			log.Info("workspace not found, skipping uninstall for module",
				"module", module.Name,
				"namespace", module.Namespace,
				"workspace", module.Spec.Workspace.Name)
			return nil
		}
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	// If workspace is being deleted, skip ready/phase checks and proceed with uninstall
	// Both resources are being cleaned up anyway
	if workspace.DeletionTimestamp == nil {
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
	}

	utils.InitMap(&module.Annotations)

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]
	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with uninstall even if metadata parsing fails, as it might not be critical for uninstall
		}
	}

	configMap := make(map[string]any)
	if module.Spec.Config != nil && module.Spec.Config.Raw != nil {
		if err := json.Unmarshal(module.Spec.Config.Raw, &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		metaData, configMap,
	); err != nil {
		return err
	} else {
		if err := iManager.Uninstall(ctx, metaData); err != nil {
			return fmt.Errorf("failed to uninstall module: %w", err)
		}
		return nil
	}
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

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with sleep even if metadata parsing fails, as it might not be critical for sleep
		}
	}

	configMap := make(map[string]any)
	if module.Spec.Config != nil && module.Spec.Config.Raw != nil {
		if err := json.Unmarshal(module.Spec.Config.Raw, &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		metaData, configMap,
	); err != nil {
		return err
	} else {
		sleepErr := iManager.Sleep(ctx, metaData)

		patch := client.MergeFrom(module.DeepCopy())
		module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData] = metaData.String()
		if err := r.Patch(ctx, module, patch); err != nil {
			log.Error(err,
				"failed to patch module with meta data annotations after sleep",
				"module", module.Name, "namespace", module.Namespace,
			)
		}

		if sleepErr != nil {
			return fmt.Errorf("failed to sleep module: %w", sleepErr)
		}
		return nil
	}
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

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with resume even if metadata parsing fails, as it might not be critical for resume
		}
	}

	configMap := make(map[string]any)
	if module.Spec.Config != nil && module.Spec.Config.Raw != nil {
		if err := json.Unmarshal(module.Spec.Config.Raw, &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		metaData, configMap,
	); err != nil {
		return err
	} else {
		resumeErr := iManager.Resume(ctx, metaData)

		patch := client.MergeFrom(module.DeepCopy())
		module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData] = metaData.String()
		if err := r.Patch(ctx, module, patch); err != nil {
			log.Error(err,
				"failed to patch module with meta data annotations after resume",
				"module", module.Name, "namespace", module.Namespace,
			)
		}

		if resumeErr != nil {
			return fmt.Errorf("failed to resume module: %w", resumeErr)
		}
		return nil
	}
}

func (r *ModuleReconciler) adoptExistingHelmRelease(
	ctx context.Context,
	workspace *batchv1.Workspace,
	module *batchv1.Module,
) error {
	ref := module.Spec.Helm.ExistingRelease

	// Create Helm service to verify and retrieve release information
	helmService, err := r.newHelmService(ctx, &workspace.Spec.Connection)
	if err != nil {
		return fmt.Errorf("failed to create Helm service: %w", err)
	}

	// Verify the release exists
	release, err := helmService.GetRelease(ref.Name, ref.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get release: %w", err)
	}

	patch := client.MergeFrom(module.DeepCopy())

	oldValues := []batchv1.ModuleSpecHelmValues{}
	if release.Chart != nil && len(release.Chart.Values) > 0 {
		chartValuesBytes, err := json.Marshal(release.Chart.Values)
		if err != nil {
			return fmt.Errorf("failed to marshal chart values to JSON: %w", err)
		}

		oldValues = append(oldValues,
			batchv1.ModuleSpecHelmValues{
				Raw: &runtime.RawExtension{
					Raw: chartValuesBytes,
				},
			},
		)
	}

	if len(release.Config) > 0 {
		configBytes, err := json.Marshal(release.Config)
		if err != nil {
			return fmt.Errorf("failed to marshal release config to JSON: %w", err)
		}

		oldValues = append(oldValues,
			batchv1.ModuleSpecHelmValues{
				Raw: &runtime.RawExtension{
					Raw: configBytes,
				},
			},
		)
	}

	module.Spec.Helm.Values = append(oldValues, module.Spec.Helm.Values...)

	if err = r.Patch(ctx, module, patch); err != nil {
		return fmt.Errorf("failed to patch module with adoption annotations: %w", err)
	}

	return nil
}
