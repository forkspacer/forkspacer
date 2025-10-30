package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/services"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *WorkspaceReconciler) forkWorkspace(
	ctx context.Context,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	modules, err := r.getRelatedModules(ctx, sourceWorkspace.Namespace, sourceWorkspace.Name)
	if err != nil {
		return fmt.Errorf(
			"failed to get related modules for source workspace %s/%s: %w",
			sourceWorkspace.Namespace, sourceWorkspace.Name, err,
		)
	}

	// Check that all source modules are ready before forking
	// This prevents forking modules that haven't finished adoption/installation
	for _, module := range modules.Items {
		if module.Status.Phase != batchv1.ModulePhaseReady && module.Status.Phase != batchv1.ModulePhaseSleeped {
			return fmt.Errorf(
				"source module %s/%s is not ready (phase: %s), cannot fork yet, will retry",
				module.Namespace, module.Name, module.Status.Phase,
			)
		}
	}

	for _, module := range modules.Items {
		sourceCopy := module.DeepCopy()
		go func() {
			newModule := &batchv1.Module{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    sourceCopy.Namespace,
					GenerateName: sourceCopy.Name + "-",
					Annotations:  make(map[string]string),
				},
				Config: sourceCopy.Config,
				Spec: batchv1.ModuleSpec{
					Helm:   sourceCopy.Spec.Helm,
					Custom: sourceCopy.Spec.Custom,
					Workspace: batchv1.ModuleWorkspaceReference{
						Name:      destWorkspace.Name,
						Namespace: destWorkspace.Namespace,
					},
					Config:     sourceCopy.Spec.Config,
					Hibernated: sourceCopy.Spec.Hibernated,
				},
			}

			if newModule.Spec.Helm != nil && newModule.Spec.Helm.ExistingRelease != nil {
				newModule.Spec.Helm.ExistingRelease = nil
			}

			if err = r.Create(ctx, newModule); err != nil {
				log.Error(err,
					"failed to create module from source workspace",
					"module_name", module.Name,
					"module_namespace", module.Namespace,
				)
				return
			}

			time.Sleep(time.Millisecond * 500)

		waitForInitialNewModuleState:
			for range 180 {
				if err := r.Get(ctx, client.ObjectKeyFromObject(newModule), newModule); err != nil {
					log.Error(err,
						"failed to get new module during initial creation wait",
						"module_name", newModule.Name,
						"module_namespace", newModule.Namespace,
					)
					time.Sleep(time.Second * 3)
					continue
				}

				switch newModule.Status.Phase {
				case batchv1.ModulePhaseReady, batchv1.ModulePhaseSleeped:
					break waitForInitialNewModuleState
				case "", batchv1.ModulePhaseInstalling, batchv1.ModulePhaseSleeping, batchv1.ModulePhaseResuming:
					time.Sleep(time.Second * 3)
					continue
				case batchv1.ModulePhaseUninstalling, batchv1.ModulePhaseFailed:
					return
				default:
					log.Error(
						errors.New("unexpected new module phase encountered"),
						"encountered unexpected new module phase during initial creation wait",
						"module_name", newModule.Name,
						"module_namespace", newModule.Namespace,
						"phase", newModule.Status.Phase,
					)
					return
				}
			}

			switch newModule.Status.Phase {
			case batchv1.ModulePhaseReady, batchv1.ModulePhaseSleeped:
			default:
				log.Error(
					errors.New("new module did not become ready or hibernated within the expected timeout"),
					"timed out waiting for new module to become ready or hibernated",
					"module_name", newModule.Name,
					"module_namespace", newModule.Namespace,
					"final_phase", newModule.Status.Phase,
				)
				return
			}

			if destWorkspace.Spec.From.MigrateData {
				if err := r.migrateModuleData(ctx, &module, newModule, sourceWorkspace, destWorkspace); err != nil {
					log.Error(err, "failed to migrate module data", "module_name", module.Name, "module_namespace", module.Namespace)
					r.Recorder.Event(destWorkspace, "Warning", "DataMigration",
						fmt.Sprintf(
							"Failed to migrate data for new module %s/%s (from source %s/%s): %v",
							newModule.Namespace, newModule.Name, module.Namespace, module.Name, err,
						),
					)
				}
			}
		}()
	}

	return nil
}

func (r *WorkspaceReconciler) migrateModuleData(
	ctx context.Context,
	sourceModule, destModule *batchv1.Module,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	// Only Helm modules support data migration
	if sourceModule.Spec.Helm == nil {
		return nil
	}

	// Render the Helm specs to get actual resource names (not templates)
	sourceHelmModule, err := sourceModule.RenderHelmSpec()
	if err != nil {
		return fmt.Errorf("failed to render source module Helm spec: %w", err)
	}

	destHelmModule, err := destModule.RenderHelmSpec()
	if err != nil {
		return fmt.Errorf("failed to render destination module Helm spec: %w", err)
	}

	// Check if PVC migration is enabled
	if len(sourceHelmModule.Migration.PVCs) == 0 {
		return nil
	}

	// Hibernate destination module and wait for it to sleep
	if err := r.hibernateModuleAndWait(ctx, destModule, "destination"); err != nil {
		return err
	}

	defer func() {
		patch := client.MergeFrom(destModule.DeepCopy())
		destModule.Spec.Hibernated = false
		if err := r.Patch(ctx, destModule, patch); err != nil {
			log.Error(err, "failed to update destination module to hibernate state")
		}
	}()

	// Hibernate source module and wait for it to sleep
	sourceModuleOldHibernationStatus := sourceModule.Spec.Hibernated
	if err := r.hibernateModuleAndWait(ctx, sourceModule, "source"); err != nil {
		return err
	}

	defer func() {
		if sourceModule.Spec.Hibernated != sourceModuleOldHibernationStatus {
			patch := client.MergeFrom(sourceModule.DeepCopy())
			sourceModule.Spec.Hibernated = sourceModuleOldHibernationStatus
			if err := r.Patch(ctx, sourceModule, patch); err != nil {
				log.Error(err, "failed to restore source module hibernation state")
			}
		}
	}()

	// Perform PVC migration
	if err := r.migratePVCs(ctx, sourceHelmModule, destHelmModule, sourceWorkspace, destWorkspace); err != nil {
		return fmt.Errorf("failed to migrate PVCs: %w", err)
	}

	// Perform Secret migration
	if err := r.migrateSecrets(
		ctx, sourceHelmModule, destHelmModule, sourceWorkspace, destWorkspace, destModule,
	); err != nil {
		return fmt.Errorf("failed to migrate Secrets: %w", err)
	}

	// Perform ConfigMap migration
	if err := r.migrateConfigMaps(ctx, sourceHelmModule, destHelmModule, sourceWorkspace, destWorkspace); err != nil {
		return fmt.Errorf("failed to migrate ConfigMaps: %w", err)
	}

	return nil
}

// migratePVCs handles the migration of PVCs from source to destination module
func (r *WorkspaceReconciler) migratePVCs(
	ctx context.Context,
	sourceHelmModule, destHelmModule *batchv1.ModuleSpecHelm,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	if len(sourceHelmModule.Migration.PVCs) == 0 {
		return nil
	}

	// Create PVC migration service
	pvcMigrationService := services.NewPVCMigrationService(&log)

	// Get kubeconfig for destination workspace
	destKubeConfig, err := NewAPIConfig(ctx, &destWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get destination kubeconfig: %w", err)
	}

	// Get kubeconfig for source workspace
	sourceKubeConfig, err := NewAPIConfig(ctx, &sourceWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get source kubeconfig: %w", err)
	}

	// Migrate each PVC
	for i, sourcePVCName := range sourceHelmModule.Migration.PVCs {
		if err := pvcMigrationService.MigratePVC(ctx,
			sourceKubeConfig, sourcePVCName, sourceHelmModule.GetNamespace(),
			destKubeConfig, destHelmModule.Migration.PVCs[i], destHelmModule.GetNamespace(),
		); err != nil {
			log.Error(err, "failed to migrate PVC",
				"sourcePVC", sourcePVCName,
				"sourceNamespace", sourceHelmModule.GetNamespace(),
				"destinationPVC", destHelmModule.Migration.PVCs[i],
				"destinationNamespace", destHelmModule.GetNamespace())
		}
	}

	return nil
}

// migrateSecrets handles the migration of Secrets from source to destination module
func (r *WorkspaceReconciler) migrateSecrets(
	ctx context.Context,
	sourceHelmModule, destHelmModule *batchv1.ModuleSpecHelm,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
	destModule *batchv1.Module,
) error {
	log := logf.FromContext(ctx)

	if len(sourceHelmModule.Migration.Secrets) == 0 {
		return nil
	}

	// Create Secret migration service
	secretMigrationService := services.NewSecretMigrationService(&log)

	// Get kubeconfig for destination workspace
	destKubeConfig, err := NewAPIConfig(ctx, &destWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get destination kubeconfig: %w", err)
	}

	// Get kubeconfig for source workspace
	sourceKubeConfig, err := NewAPIConfig(ctx, &sourceWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get source kubeconfig: %w", err)
	}

	destReleaseName := destModule.Spec.Helm.GetReleaseName()

	// Migrate each Secret
	for i, sourceSecretName := range sourceHelmModule.Migration.Secrets {
		if err := secretMigrationService.MigrateSecret(ctx,
			sourceKubeConfig, sourceSecretName, sourceHelmModule.GetNamespace(),
			destKubeConfig, destHelmModule.Migration.Secrets[i], destHelmModule.GetNamespace(),
			destReleaseName,
		); err != nil {
			log.Error(err, "failed to migrate Secret",
				"sourceSecret", sourceSecretName,
				"sourceNamespace", sourceHelmModule.GetNamespace(),
				"destinationSecret", destHelmModule.Migration.Secrets[i],
				"destinationNamespace", destHelmModule.GetNamespace())
		}
	}

	return nil
}

// migrateConfigMaps handles the migration of ConfigMaps from source to destination module
func (r *WorkspaceReconciler) migrateConfigMaps(
	ctx context.Context,
	sourceHelmModule, destHelmModule *batchv1.ModuleSpecHelm,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	if len(sourceHelmModule.Migration.ConfigMaps) == 0 {
		return nil
	}

	// Create ConfigMap migration service
	configMapMigrationService := services.NewConfigMapMigrationService(&log)

	// Get kubeconfig for destination workspace
	destKubeConfig, err := NewAPIConfig(ctx, &destWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get destination kubeconfig: %w", err)
	}

	// Get kubeconfig for source workspace
	sourceKubeConfig, err := NewAPIConfig(ctx, &sourceWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get source kubeconfig: %w", err)
	}

	// Migrate each ConfigMap
	for i, sourceConfigMapName := range sourceHelmModule.Migration.ConfigMaps {
		if err := configMapMigrationService.MigrateConfigMap(ctx,
			sourceKubeConfig, sourceConfigMapName, sourceHelmModule.GetNamespace(),
			destKubeConfig, destHelmModule.Migration.ConfigMaps[i], destHelmModule.GetNamespace(),
		); err != nil {
			log.Error(err, "failed to migrate ConfigMap",
				"sourceConfigMap", sourceConfigMapName,
				"sourceNamespace", sourceHelmModule.GetNamespace(),
				"destinationConfigMap", destHelmModule.Migration.ConfigMaps[i],
				"destinationNamespace", destHelmModule.GetNamespace())
		}
	}

	return nil
}

// applyNamespacePrefix applies a namespace prefix to module resources
// func (r *WorkspaceReconciler) applyNamespacePrefix(moduleData []byte, prefix string) ([]byte, error) {
// 	configMap := make(map[string]any)
// 	var updatedData []byte

// 	// Parse the module to extract and modify the namespace
// 	err := resources.HandleResource(moduleData, true, &configMap,
// 		func(helmModule resources.HelmModule) error {
// 			helmModule.Spec.Namespace = prefix + helmModule.Spec.Namespace
// 			data, err := yaml.Marshal(helmModule)
// 			if err != nil {
// 				return fmt.Errorf("failed to marshal updated Helm module: %w", err)
// 			}
// 			updatedData = data
// 			return nil
// 		},
// 		func(customModule resources.CustomModule) error {
// 			// Custom modules don't have a namespace field
// 			// No prefix needed for custom modules
// 			updatedData = moduleData
// 			return nil
// 		},
// 	)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to apply namespace prefix: %w", err)
// 	}

// 	return updatedData, nil
// }
