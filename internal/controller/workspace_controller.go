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
	"sync"
	"time"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sTypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	cronCons "github.com/forkspacer/forkspacer/pkg/constants/cron"
	"github.com/forkspacer/forkspacer/pkg/utils"
	"github.com/go-logr/logr"
	"github.com/robfig/cron/v3"
)

var workspaceFinalizers = struct {
	Delete string
}{
	Delete: "delete",
}

type sleepCronManager struct {
	sync.RWMutex
	idMapper map[k8sTypes.UID]cron.EntryID
	cron     *cron.Cron
}

type resumeCronManager struct {
	sync.RWMutex
	idMapper map[k8sTypes.UID]cron.EntryID
	cron     *cron.Cron
}

// WorkspaceReconciler reconciles a Workspace object
type WorkspaceReconciler struct {
	client.Client
	sleepCronManager  *sleepCronManager
	resumeCronManager *resumeCronManager
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
}

// +kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	workspace := &batchv1.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, workspace); err != nil {
		if k8sErrors.IsNotFound(err) {
			log.Info("Workspace resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Workspace")
		return ctrl.Result{}, err
	}

	if workspace.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, workspace)
	}

	switch workspace.Status.Phase {
	case "":
		rslt, err := r.handleEmptyPhase(ctx, workspace)
		if err != nil {
			return rslt, err
		}

		return r.handleAutoHibernation(ctx, workspace), nil

	case batchv1.WorkspacePhaseReady:
		rslt, err := r.handleHibernation(ctx, workspace)
		if err != nil {
			return rslt, err
		}

		return r.handleAutoHibernation(ctx, workspace), nil

	case batchv1.WorkspacePhaseHibernated:
		rslt, err := r.handleHibernation(ctx, workspace)
		if err != nil {
			return rslt, err
		}

		return r.handleAutoHibernation(ctx, workspace), nil

	case batchv1.WorkspacePhaseFailed, batchv1.WorkspacePhaseTerminating:
		return ctrl.Result{}, nil

	default:
		log.Error(errors.New("unknown workspace phase"),
			"encountered unknown workspace phase",
			"phase", workspace.Status.Phase,
		)
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.sleepCronManager = &sleepCronManager{
		idMapper: make(map[k8sTypes.UID]cron.EntryID),
		cron:     cron.New(cron.WithParser(cron.NewParser(cronCons.CronParserOptions))),
	}

	r.resumeCronManager = &resumeCronManager{
		idMapper: make(map[k8sTypes.UID]cron.EntryID),
		cron:     cron.New(cron.WithParser(cron.NewParser(cronCons.CronParserOptions))),
	}

	r.sleepCronManager.cron.Start()
	r.resumeCronManager.cron.Start()

	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.Workspace{}).
		Named("workspace").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		Complete(r)
}

func (r *WorkspaceReconciler) addSleepCron(log logr.Logger, workspace *batchv1.Workspace, override bool) error {
	r.sleepCronManager.Lock()
	defer r.sleepCronManager.Unlock()

	if entryID, ok := r.sleepCronManager.idMapper[workspace.UID]; ok {
		if override {
			newSchedule, err := cron.NewParser(cronCons.CronParserOptions).Parse(workspace.Spec.AutoHibernation.Schedule)
			if err != nil {
				return err
			}

			entry := r.sleepCronManager.cron.Entry(entryID)
			now := time.Now()

			if entry.Schedule.Next(now).Equal(newSchedule.Next(now)) {
				return nil
			}

			r.removeSleepCron(workspace.UID)
		} else {
			return fmt.Errorf("sleep cron entry for workspace %s/%s already exists", workspace.Namespace, workspace.Name)
		}
	}

	entryID, err := r.sleepCronManager.cron.AddFunc(
		workspace.Spec.AutoHibernation.Schedule,
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
			defer cancel()

			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				log.Error(err, "failed to fetch workspace in sleep cron job")
				return
			}

			if !workspace.Spec.Hibernated {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
				defer cancel()

				patch := client.MergeFrom(workspace.DeepCopy())
				workspace.Spec.Hibernated = true
				if err := r.Patch(ctx, workspace, patch); err != nil {
					log.Error(err, "failed to update workspace to hibernate state")
				}
			}
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add sleep cron entry for workspace %s/%s: %w", workspace.Namespace, workspace.Name, err)
	}

	r.sleepCronManager.idMapper[workspace.UID] = entryID

	return nil
}

func (r *WorkspaceReconciler) removeSleepCron(workspaceUID k8sTypes.UID) {
	r.sleepCronManager.Lock()
	defer r.sleepCronManager.Unlock()

	entryID, ok := r.sleepCronManager.idMapper[workspaceUID]
	if !ok {
		return
	}

	r.sleepCronManager.cron.Remove(entryID)
	delete(r.sleepCronManager.idMapper, workspaceUID)
}

func (r *WorkspaceReconciler) addResumeCron(log logr.Logger, workspace *batchv1.Workspace, override bool) error {
	r.resumeCronManager.Lock()
	defer r.resumeCronManager.Unlock()

	if entryID, ok := r.resumeCronManager.idMapper[workspace.UID]; ok {
		if override {
			newSchedule, err := cron.NewParser(cronCons.CronParserOptions).Parse(*workspace.Spec.AutoHibernation.WakeSchedule)
			if err != nil {
				return err
			}

			entry := r.resumeCronManager.cron.Entry(entryID)
			now := time.Now()

			if entry.Schedule.Next(now).Equal(newSchedule.Next(now)) {
				return nil
			}

			r.removeResumeCron(workspace.UID)
		} else {
			return fmt.Errorf("resume cron entry for workspace %s/%s already exists", workspace.Namespace, workspace.Name)
		}
	}

	entryID, err := r.resumeCronManager.cron.AddFunc(
		*workspace.Spec.AutoHibernation.WakeSchedule,
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
			defer cancel()

			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				log.Error(err, "failed to fetch workspace in resume cron job")
				return
			}

			if workspace.Spec.Hibernated {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
				defer cancel()

				patch := client.MergeFrom(workspace.DeepCopy())
				workspace.Spec.Hibernated = false
				if err := r.Patch(ctx, workspace, patch); err != nil {
					log.Error(err, "failed to update workspace to hibernate state")
				}
			}
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add resume cron entry for workspace %s/%s: %w", workspace.Namespace, workspace.Name, err)
	}

	r.resumeCronManager.idMapper[workspace.UID] = entryID

	return nil
}

func (r *WorkspaceReconciler) removeResumeCron(workspaceUID k8sTypes.UID) {
	r.resumeCronManager.Lock()
	defer r.resumeCronManager.Unlock()

	entryID, ok := r.resumeCronManager.idMapper[workspaceUID]
	if !ok {
		return
	}

	r.resumeCronManager.cron.Remove(entryID)
	delete(r.resumeCronManager.idMapper, workspaceUID)
}

func (r *WorkspaceReconciler) handleEmptyPhase(
	ctx context.Context,
	workspace *batchv1.Workspace,
) (ctrl.Result, error) { //nolint:unparam
	log := logf.FromContext(ctx)

	err := retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				return err
			}

			if controllerutil.ContainsFinalizer(workspace, workspaceFinalizers.Delete) {
				return nil
			}

			controllerutil.AddFinalizer(workspace, workspaceFinalizers.Delete)
			return r.Update(ctx, workspace)
		},
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Handle managed workspace setup
	if workspace.Spec.Type == batchv1.WorkspaceTypeManaged {
		log.Info("Setting up managed workspace",
			"workspace", workspace.Name,
			"namespace", workspace.Namespace)

		// Set phase to installing
		if err := r.setPhaseInstalling(ctx, workspace, utils.ToPtr("Installing virtual cluster")); err != nil {
			return ctrl.Result{}, err
		}

		// Setup connection configuration before installing vcluster
		r.setupManagedWorkspaceConnection(workspace)

		// Update workspace with connection configuration
		if err := r.Update(ctx, workspace); err != nil {
			log.Error(err, "failed to update workspace with connection configuration")
			return ctrl.Result{}, err
		}

		// Install vcluster
		if err := r.installVCluster(ctx, workspace); err != nil {
			log.Error(err, "failed to install virtual cluster for managed workspace")
			r.Recorder.Event(workspace, "Warning", "VirtualClusterInstallError",
				fmt.Sprintf("Failed to install virtual cluster: %v", err))
			if err := r.setPhaseFailed(
				ctx, workspace,
				utils.ToPtr("Failed to install virtual cluster. Check events for more information."),
			); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}

		r.Recorder.Event(workspace, "Normal", "VirtualClusterInstalled",
			"Successfully installed virtual cluster for managed workspace")
	}

	log.Info("New workspace detected, setting initial status")
	if workspace.Spec.Hibernated {
		if err := r.setPhaseHibernated(ctx, workspace, nil); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err := r.setPhaseReady(ctx, workspace, nil); err != nil {
			return ctrl.Result{}, err
		}

		if workspace.Spec.From != nil {
			fromWorkspace := &batchv1.Workspace{}
			if err := r.Get(ctx,
				client.ObjectKey{
					Namespace: workspace.Spec.From.Namespace,
					Name:      workspace.Spec.From.Name,
				}, fromWorkspace,
			); err != nil {
				r.Recorder.Event(workspace, "Warning", "Start",
					fmt.Sprintf(
						"failed to get source workspace %s/%s: %s",
						workspace.Spec.From.Namespace, workspace.Spec.From.Name, err.Error(),
					),
				)

				if err := r.setPhaseFailed(
					ctx, workspace,
					utils.ToPtr("Failed to start workspace. Check events for more information."),
				); err != nil {
					log.Error(err, "failed to update workspace status to Failed after start workspace error")
					return ctrl.Result{}, err
				}

				return ctrl.Result{}, fmt.Errorf(
					"failed to get source workspace %s/%s: %w",
					workspace.Spec.From.Namespace, workspace.Spec.From.Name, err,
				)
			}

			if err := r.forkWorkspace(ctx, fromWorkspace, workspace); err != nil {
				// Log the error and requeue - modules might not be ready yet
				log.Info("fork workspace failed, will retry",
					"source_workspace", fmt.Sprintf("%s/%s", fromWorkspace.Namespace, fromWorkspace.Name),
					"dest_workspace", fmt.Sprintf("%s/%s", workspace.Namespace, workspace.Name),
					"error", err.Error())
				return ctrl.Result{RequeueAfter: time.Second * 5}, nil
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *WorkspaceReconciler) handleDeletion(ctx context.Context, workspace *batchv1.Workspace) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	switch workspace.Status.Phase {
	case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, batchv1.WorkspacePhaseFailed:
	default:
		return ctrl.Result{RequeueAfter: time.Second * 2}, nil
	}

	modules, err := r.getRelatedModules(ctx, workspace.Namespace, workspace.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.removeSleepCron(workspace.UID)
	r.removeResumeCron(workspace.UID)

	// Uninstall vcluster for managed workspaces
	if workspace.Spec.Type == batchv1.WorkspaceTypeManaged {
		log.Info("Cleaning up managed workspace",
			"workspace", workspace.Name,
			"namespace", workspace.Namespace)

		if err := r.uninstallVCluster(ctx, workspace); err != nil {
			log.Error(err, "failed to uninstall vcluster for managed workspace")
			r.Recorder.Event(workspace, "Warning", "VClusterUninstallError",
				fmt.Sprintf("Failed to uninstall vcluster: %v", err))
			// Continue with deletion even if vcluster uninstall fails
		} else {
			r.Recorder.Event(workspace, "Normal", "VClusterUninstalled",
				"Successfully uninstalled vcluster for managed workspace")
		}
	}

	// Delete all modules and wait for deletion with timeout
	for _, module := range modules.Items {
		moduleCopy := module // Avoid loop variable capture

		// Attempt to delete the module
		if err := r.Delete(ctx, &moduleCopy); err != nil {
			if !k8sErrors.IsNotFound(err) {
				log.Error(err, "failed to delete module", "namespace", moduleCopy.Namespace, "name", moduleCopy.Name)
			}
			// Continue to next module even if deletion fails
			continue
		}

		// Wait up to 60 seconds for module to be deleted
		deletionTimeout := time.Now().Add(60 * time.Second)
		deleted := false

		for time.Now().Before(deletionTimeout) {
			if err := r.Get(ctx, client.ObjectKeyFromObject(&moduleCopy), &moduleCopy); err != nil {
				if k8sErrors.IsNotFound(err) {
					deleted = true
					break
				}
				log.Error(err, "error checking module deletion status", "namespace", moduleCopy.Namespace, "name", moduleCopy.Name)
			}
			time.Sleep(2 * time.Second)
		}

		if !deleted {
			log.Info("module deletion timed out, proceeding with workspace deletion",
				"namespace", moduleCopy.Namespace, "name", moduleCopy.Name,
				"phase", moduleCopy.Status.Phase)
			r.Recorder.Event(workspace, "Warning", "ModuleDeletionTimeout",
				fmt.Sprintf("Module %s/%s deletion timed out. Manual cleanup may be required.",
					moduleCopy.Namespace, moduleCopy.Name))
		}
	}

	return ctrl.Result{}, retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				return err
			}

			if !controllerutil.ContainsFinalizer(workspace, workspaceFinalizers.Delete) {
				return nil
			}

			controllerutil.RemoveFinalizer(workspace, workspaceFinalizers.Delete)
			return r.Update(ctx, workspace)
		},
	)
}

func (r *WorkspaceReconciler) handleHibernation(
	ctx context.Context,
	workspace *batchv1.Workspace,
) (ctrl.Result, error) { //nolint:unparam
	log := logf.FromContext(ctx)

	// Sleep
	if workspace.Status.HibernatedAt == nil && workspace.Spec.Hibernated {
		err := r.sleepModules(ctx, workspace)
		if err != nil {
			if err := r.setPhaseFailed(
				ctx, workspace,
				utils.ToPtr("Workspace hibernation failed. Check events for more information."),
			); err != nil {
				log.Error(err, "failed to update workspace status to Failed after sleep modules error")
				return ctrl.Result{}, err
			}
			r.Recorder.Event(workspace, "Warning", "HibernateError", fmt.Sprintf("Workspace hibernation failed: %v", err))
		} else {
			if err := r.setPhaseHibernated(ctx, workspace, nil); err != nil {
				log.Error(err, "failed to update workspace status to Hibernated error")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, err
	}

	// Resume
	if workspace.Status.HibernatedAt != nil && !workspace.Spec.Hibernated {
		err := r.resumeModules(ctx, workspace)
		if err != nil {
			if err := r.setPhaseFailed(
				ctx, workspace,
				utils.ToPtr("Workspace resume failed. Check events for more information."),
			); err != nil {
				log.Error(err, "failed to update workspace status to Failed after resume modules error")
				return ctrl.Result{}, err
			}
			r.Recorder.Event(workspace, "Warning", "HibernateError", fmt.Sprintf("Workspace hibernation failed: %v", err))
		} else {
			if err := r.setPhaseReady(ctx, workspace, nil); err != nil {
				log.Error(err, "failed to update workspace status to Ready error")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WorkspaceReconciler) handleAutoHibernation(
	ctx context.Context,
	workspace *batchv1.Workspace,
) ctrl.Result { //nolint:unparam
	log := logf.FromContext(ctx)

	if workspace.Spec.AutoHibernation != nil {
		if workspace.Spec.AutoHibernation.Enabled {
			if err := r.addSleepCron(log, workspace, true); err != nil {
				log.Error(err,
					"Failed to add sleep cron schedule for workspace",
					"namespace", workspace.Namespace,
					"name", workspace.Name,
				)
				r.Recorder.Event(workspace, "Warning", "HibernationCron",
					fmt.Sprintf("Failed to add auto-hibernation schedule: %v", err),
				)
			}

			if workspace.Spec.AutoHibernation.WakeSchedule != nil {
				if err := r.addResumeCron(log, workspace, true); err != nil {
					log.Error(err,
						"Failed to add resume cron schedule for workspace",
						"namespace", workspace.Namespace,
						"name", workspace.Name,
					)
					r.Recorder.Event(workspace, "Warning", "HibernationCron",
						fmt.Sprintf("Failed to add auto-resume schedule: %v", err),
					)
				}
			} else {
				r.removeResumeCron(workspace.UID)
			}
		} else {
			r.removeSleepCron(workspace.UID)
			r.removeResumeCron(workspace.UID)
		}
	}

	return ctrl.Result{}
}

// hibernateModuleAndWait hibernates a module and waits for it to reach the sleeped state
func (r *WorkspaceReconciler) hibernateModuleAndWait(
	ctx context.Context,
	module *batchv1.Module,
	moduleType string,
) error {
	log := logf.FromContext(ctx)

	if !module.Spec.Hibernated {
		patch := client.MergeFrom(module.DeepCopy())
		module.Spec.Hibernated = true
		if err := r.Patch(ctx, module, patch); err != nil {
			return err
		}
	}

	for range 180 {
		if err := r.Get(ctx, client.ObjectKeyFromObject(module), module); err != nil {
			log.Error(err,
				fmt.Sprintf("failed to get %s module during hibernation wait", moduleType),
				"module_name", module.Name,
				"module_namespace", module.Namespace,
			)
			time.Sleep(time.Second * 3)
			continue
		}

		switch module.Status.Phase {
		case batchv1.ModulePhaseSleeped:
			return nil
		case "", batchv1.ModulePhaseReady, batchv1.ModulePhaseInstalling,
			batchv1.ModulePhaseSleeping, batchv1.ModulePhaseResuming:
			time.Sleep(time.Second * 3)
			continue
		case batchv1.ModulePhaseUninstalling:
			return fmt.Errorf(
				"%s module %s/%s is uninstalling, cannot proceed with hibernation",
				moduleType, module.Namespace, module.Name,
			)
		case batchv1.ModulePhaseFailed:
			return fmt.Errorf(
				"%s module %s/%s has failed, cannot proceed with hibernation",
				moduleType, module.Namespace, module.Name,
			)
		default:
			return fmt.Errorf(
				"unexpected %s module phase '%s' encountered during hibernation wait for module %s/%s",
				moduleType, module.Status.Phase, module.Namespace, module.Name,
			)
		}
	}

	if module.Status.Phase != batchv1.ModulePhaseSleeped {
		return fmt.Errorf(
			"timed out waiting for %s module %s/%s to become hibernated, current phase: %s",
			moduleType, module.Namespace, module.Name, module.Status.Phase,
		)
	}

	return nil
}

func (r *WorkspaceReconciler) sleepModules(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	if workspace.Status.HibernatedAt != nil {
		return nil
	}

	modules, err := r.getRelatedModules(ctx, workspace.Namespace, workspace.Name)
	if err != nil {
		return err
	}

	for _, module := range modules.Items {
		if !module.Spec.Hibernated {
			patch := client.MergeFrom(module.DeepCopy())
			module.Spec.Hibernated = true
			if err := r.Patch(ctx, &module, patch); err != nil {
				log.Error(err, "failed to update module to hibernate state")
			}
		}
	}

	return nil
}

func (r *WorkspaceReconciler) resumeModules(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	if workspace.Status.HibernatedAt == nil {
		return nil
	}

	modules, err := r.getRelatedModules(ctx, workspace.Namespace, workspace.Name)
	if err != nil {
		return err
	}

	for _, module := range modules.Items {
		if !module.Spec.Hibernated {
			patch := client.MergeFrom(module.DeepCopy())
			module.Spec.Hibernated = false
			if err := r.Patch(ctx, &module, patch); err != nil {
				log.Error(err, "failed to update module to hibernate state")
			}
		}
	}

	return nil
}

func (r *WorkspaceReconciler) getRelatedModules(
	ctx context.Context,
	workspaceNamespace, workspaceName string,
) (*batchv1.ModuleList, error) {
	modules := &batchv1.ModuleList{}
	if err := r.List(
		ctx, modules,
		client.MatchingFields{
			"spec.workspace.namespace": workspaceNamespace,
			"spec.workspace.name":      workspaceName,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to get modules for workspace %s/%s: %w", workspaceNamespace, workspaceName, err)
	}

	return modules, nil
}

func (r *WorkspaceReconciler) setPhaseReady(ctx context.Context, workspace *batchv1.Workspace, message *string) error {
	return retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				return err
			}

			workspace.Status = batchv1.WorkspaceStatus{
				Phase:        batchv1.WorkspacePhaseReady,
				Ready:        true,
				LastActivity: &metav1.Time{Time: time.Now()},
				HibernatedAt: nil,
				Message:      message,
			}

			return r.Status().Update(ctx, workspace)
		},
	)
}

func (r *WorkspaceReconciler) setPhaseFailed(ctx context.Context, workspace *batchv1.Workspace, message *string) error {
	return retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				return err
			}

			workspace.Status = batchv1.WorkspaceStatus{
				Phase:        batchv1.WorkspacePhaseFailed,
				Ready:        false,
				LastActivity: &metav1.Time{Time: time.Now()},
				HibernatedAt: nil,
				Message:      message,
			}

			return r.Status().Update(ctx, workspace)
		},
	)
}

func (r *WorkspaceReconciler) setPhaseHibernated(
	ctx context.Context,
	workspace *batchv1.Workspace,
	message *string,
) error {
	return retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				return err
			}

			workspace.Status = batchv1.WorkspaceStatus{
				Phase:        batchv1.WorkspacePhaseHibernated,
				Ready:        true,
				LastActivity: &metav1.Time{Time: time.Now()},
				HibernatedAt: &metav1.Time{Time: time.Now()},
				Message:      message,
			}

			return r.Status().Update(ctx, workspace)
		},
	)
}

func (r *WorkspaceReconciler) setPhaseInstalling(
	ctx context.Context,
	workspace *batchv1.Workspace,
	message *string,
) error {
	return retry.RetryOnConflict(
		retry.DefaultRetry,
		func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(workspace), workspace); err != nil {
				return err
			}

			workspace.Status = batchv1.WorkspaceStatus{
				Phase:        batchv1.WorkspacePhaseInstalling,
				Ready:        false,
				LastActivity: &metav1.Time{Time: time.Now()},
				HibernatedAt: nil,
				Message:      message,
			}

			return r.Status().Update(ctx, workspace)
		},
	)
}
