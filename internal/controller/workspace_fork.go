package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
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

	const maxNameLength = 253

	for _, module := range modules.Items {
		timestamp := time.Now().Format("20060102150405")
		moduleName := module.Name

		// Truncate module name if it would exceed Kubernetes name length limit
		if len(moduleName)+len(timestamp) > maxNameLength {
			moduleName = moduleName[:maxNameLength-len(timestamp)]
		}

		go func() {
			newModule := &batchv1.Module{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   module.Namespace,
					Name:        moduleName + "-" + timestamp,
					Annotations: make(map[string]string),
				},
				Spec: batchv1.ModuleSpec{
					Source: module.Spec.Source,
					Workspace: batchv1.ModuleWorkspaceReference{
						Name:      destWorkspace.Name,
						Namespace: destWorkspace.Namespace,
					},
					Config:     module.Spec.Config,
					Hibernated: module.Spec.Hibernated,
				},
			}
			if newModule.Spec.Source.ExistingHelmRelease != nil {
				resource := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
				if resource == "" {
					log.Error(errors.New("missing Helm release resource annotation"),
						"Annotation containing raw Helm release YAML is missing for module with ExistingHelmRelease source",
						"module_name", module.Name,
						"module_namespace", module.Namespace,
					)
					return
				}

				resourceJSON, err := yaml.YAMLToJSON([]byte(resource))
				if err != nil {
					log.Error(err,
						"Failed to parse resource annotation YAML to JSON",
						"module_name", module.Name,
						"module_namespace", module.Namespace,
					)
					return
				}

				newModule.Spec.Source.Raw = &runtime.RawExtension{Raw: resourceJSON}
				newModule.Spec.Source.ExistingHelmRelease = nil
				newModule.Spec.Source.ConfigMap = nil
				newModule.Spec.Source.Github = nil
				newModule.Spec.Source.HttpURL = nil
				newModule.Spec.Config = nil
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
	if !IsHelmModule(sourceModule) || !IsHelmModule(destModule) {
		return nil
	}

	// Parse source Helm module
	sourceHelmModule, err := ParseHelmModule(ctx, sourceModule)
	if err != nil {
		return fmt.Errorf("failed to parse source Helm module: %w", err)
	}

	// Check if PVC migration is enabled
	if sourceHelmModule.Spec.Migration == nil ||
		sourceHelmModule.Spec.Migration.PVC == nil ||
		!sourceHelmModule.Spec.Migration.PVC.Enabled ||
		len(sourceHelmModule.Spec.Migration.PVC.Names) == 0 {
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

	// Parse destination Helm module
	destHelmModule, err := ParseHelmModule(ctx, destModule)
	if err != nil {
		return fmt.Errorf("failed to parse destination Helm module: %w", err)
	}

	// Perform PVC migration
	if err := r.migratePVCs(ctx, sourceHelmModule, destHelmModule, sourceWorkspace, destWorkspace); err != nil {
		return fmt.Errorf("failed to migrate PVCs: %w", err)
	}

	// Perform Secret migration
	if err := r.migrateSecrets(ctx, sourceHelmModule, destHelmModule, sourceWorkspace, destWorkspace); err != nil {
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
	sourceHelmModule, destHelmModule resources.HelmModule,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	// Check if PVC migration is enabled
	if sourceHelmModule.Spec.Migration == nil ||
		sourceHelmModule.Spec.Migration.PVC == nil ||
		!sourceHelmModule.Spec.Migration.PVC.Enabled ||
		len(sourceHelmModule.Spec.Migration.PVC.Names) == 0 {
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
	for i, sourcePVCName := range sourceHelmModule.Spec.Migration.PVC.Names {
		if err := pvcMigrationService.MigratePVC(ctx,
			sourceKubeConfig, sourcePVCName, sourceHelmModule.Spec.Namespace,
			destKubeConfig, destHelmModule.Spec.Migration.PVC.Names[i], destHelmModule.Spec.Namespace,
		); err != nil {
			log.Error(err, "failed to migrate PVC",
				"sourcePVC", sourcePVCName,
				"sourceNamespace", sourceHelmModule.Spec.Namespace,
				"destinationPVC", destHelmModule.Spec.Migration.PVC.Names[i],
				"destinationNamespace", destHelmModule.Spec.Namespace)
		}
	}

	return nil
}

// migrateSecrets handles the migration of Secrets from source to destination module
func (r *WorkspaceReconciler) migrateSecrets(
	ctx context.Context,
	sourceHelmModule, destHelmModule resources.HelmModule,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	// Check if Secret migration is enabled
	if sourceHelmModule.Spec.Migration == nil ||
		sourceHelmModule.Spec.Migration.Secret == nil ||
		!sourceHelmModule.Spec.Migration.Secret.Enabled ||
		len(sourceHelmModule.Spec.Migration.Secret.Names) == 0 {
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

	// Migrate each Secret
	for i, sourceSecretName := range sourceHelmModule.Spec.Migration.Secret.Names {
		if err := secretMigrationService.MigrateSecret(ctx,
			sourceKubeConfig, sourceSecretName, sourceHelmModule.Spec.Namespace,
			destKubeConfig, destHelmModule.Spec.Migration.Secret.Names[i], destHelmModule.Spec.Namespace,
		); err != nil {
			log.Error(err, "failed to migrate Secret",
				"sourceSecret", sourceSecretName,
				"sourceNamespace", sourceHelmModule.Spec.Namespace,
				"destinationSecret", destHelmModule.Spec.Migration.Secret.Names[i],
				"destinationNamespace", destHelmModule.Spec.Namespace)
		}
	}

	return nil
}

// migrateConfigMaps handles the migration of ConfigMaps from source to destination module
func (r *WorkspaceReconciler) migrateConfigMaps(
	ctx context.Context,
	sourceHelmModule, destHelmModule resources.HelmModule,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	// Check if ConfigMap migration is enabled
	if sourceHelmModule.Spec.Migration == nil ||
		sourceHelmModule.Spec.Migration.ConfigMap == nil ||
		!sourceHelmModule.Spec.Migration.ConfigMap.Enabled ||
		len(sourceHelmModule.Spec.Migration.ConfigMap.Names) == 0 {
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
	for i, sourceConfigMapName := range sourceHelmModule.Spec.Migration.ConfigMap.Names {
		if err := configMapMigrationService.MigrateConfigMap(ctx,
			sourceKubeConfig, sourceConfigMapName, sourceHelmModule.Spec.Namespace,
			destKubeConfig, destHelmModule.Spec.Migration.ConfigMap.Names[i], destHelmModule.Spec.Namespace,
		); err != nil {
			log.Error(err, "failed to migrate ConfigMap",
				"sourceConfigMap", sourceConfigMapName,
				"sourceNamespace", sourceHelmModule.Spec.Namespace,
				"destinationConfigMap", destHelmModule.Spec.Migration.ConfigMap.Names[i],
				"destinationNamespace", destHelmModule.Spec.Namespace)
		}
	}

	return nil
}
