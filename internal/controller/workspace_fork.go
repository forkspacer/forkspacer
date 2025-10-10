package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	"github.com/forkspacer/forkspacer/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
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

	// Create PV migration service
	pvMigrateService := services.NewPVMigrateService(nil)

	// Get kubeconfig for destination workspace
	destKubeConfig, err := NewKubernetesConfig(ctx, &destWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get destination kubeconfig: %w", err)
	}

	// Get kubeconfig for source workspace
	sourceKubeConfig, err := NewKubernetesConfig(ctx, &sourceWorkspace.Spec.Connection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get source kubeconfig: %w", err)
	}

	// Create temporary kubeconfig files
	destKubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("dest-kubeconfig-%s-*.yaml", destWorkspace.Name))
	destKubeconfigData, err := clientcmd.Write(*utils.ConvertRestConfigToAPIConfig(destKubeConfig, "", "", ""))
	if err != nil {
		return fmt.Errorf("failed to write destination kubeconfig: %w", err)
	}
	if err := os.WriteFile(destKubeconfigPath, destKubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to save destination kubeconfig: %w", err)
	}

	sourceKubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("source-kubeconfig-%s-*.yaml", sourceWorkspace.Name))
	sourceKubeconfigData, err := clientcmd.Write(*utils.ConvertRestConfigToAPIConfig(sourceKubeConfig, "", "", ""))
	if err != nil {
		return fmt.Errorf("failed to write source kubeconfig: %w", err)
	}
	if err := os.WriteFile(sourceKubeconfigPath, sourceKubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to save source kubeconfig: %w", err)
	}

	// Clean up temporary files
	defer func() {
		if err := os.Remove(destKubeconfigPath); err != nil {
			log.Error(err, "failed to remove temp destination kubeconfig file", "path", destKubeconfigPath)
		}
		if err := os.Remove(sourceKubeconfigPath); err != nil {
			log.Error(err, "failed to remove temp source kubeconfig file", "path", sourceKubeconfigPath)
		}
	}()

	// Migrate each PVC
	for i, sourcePVCName := range sourceHelmModule.Spec.Migration.PVC.Names {
		if err := pvMigrateService.MigratePVC(ctx,
			sourceKubeconfigPath, sourcePVCName, sourceHelmModule.Spec.Namespace,
			destKubeconfigPath, destHelmModule.Spec.Migration.PVC.Names[i], destHelmModule.Spec.Namespace,
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
