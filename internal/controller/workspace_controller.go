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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sTypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/manager"
	managerBase "github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	"github.com/forkspacer/forkspacer/pkg/types"
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
		log.Error(errors.New("unknown workspace phase"), "encountered unknown workspace phase", "phase", workspace.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.sleepCronManager = &sleepCronManager{
		idMapper: make(map[k8sTypes.UID]cron.EntryID),
		cron:     cron.New(cron.WithParser(cron.NewParser(types.CronParserOptions))),
	}

	r.resumeCronManager = &resumeCronManager{
		idMapper: make(map[k8sTypes.UID]cron.EntryID),
		cron:     cron.New(cron.WithParser(cron.NewParser(types.CronParserOptions))),
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
			newSchedule, err := cron.NewParser(types.CronParserOptions).Parse(workspace.Spec.AutoHibernation.Schedule)
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

			if utils.NotNilAndNot(workspace.Spec.Hibernated, true) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
				defer cancel()

				patch := client.MergeFrom(workspace.DeepCopy())
				*workspace.Spec.Hibernated = true
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
			newSchedule, err := cron.NewParser(types.CronParserOptions).Parse(*workspace.Spec.AutoHibernation.WakeSchedule)
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

			if utils.NotNilAndZero(workspace.Spec.Hibernated) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
				defer cancel()

				patch := client.MergeFrom(workspace.DeepCopy())
				*workspace.Spec.Hibernated = false
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

func (r *WorkspaceReconciler) handleEmptyPhase(ctx context.Context, workspace *batchv1.Workspace) (ctrl.Result, error) { //nolint:unparam
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

	log.Info("New workspace detected, setting initial status")
	if utils.NotNilAndZero(workspace.Spec.Hibernated) {
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
				r.Recorder.Event(workspace, "Warning", "Start", fmt.Sprintf("failed to get source workspace %s/%s: %s", workspace.Spec.From.Namespace, workspace.Spec.From.Name, err.Error()))

				if err := r.setPhaseFailed(ctx, workspace, utils.ToPtr("Failed to start workspace. Check events for more information.")); err != nil {
					log.Error(err, "failed to update workspace status to Failed after start workspace error")
					return ctrl.Result{}, err
				}

				return ctrl.Result{}, fmt.Errorf("failed to get source workspace %s/%s: %w", workspace.Spec.From.Namespace, workspace.Spec.From.Name, err)
			}

			if err := r.forkWorkspace(ctx, fromWorkspace, workspace); err != nil {
				return ctrl.Result{}, fmt.Errorf(
					"failed to fork workspace %s/%s from %s/%s: %w",
					workspace.Namespace, workspace.Name,
					fromWorkspace.Namespace, fromWorkspace.Name,
					err,
				)
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

	for _, module := range modules.Items {
	deleteLoop:
		for {
			if err := r.Delete(ctx, &module); err != nil {
				log.Error(err, "failed to delete module", "namespace", module.Namespace, "module", module.Name)
				time.Sleep(1 * time.Second)
				continue
			}
			for range 10 {
				if err := r.Get(ctx, client.ObjectKeyFromObject(&module), &module); err != nil {
					break deleteLoop
				}
				time.Sleep(2 * time.Second)
			}
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

func (r *WorkspaceReconciler) handleHibernation(ctx context.Context, workspace *batchv1.Workspace) (ctrl.Result, error) { //nolint:unparam
	log := logf.FromContext(ctx)

	// Sleep
	if workspace.Status.HibernatedAt == nil && utils.NotNilAndZero(workspace.Spec.Hibernated) {
		err := r.sleepModules(ctx, workspace)
		if err != nil {
			if err := r.setPhaseFailed(ctx, workspace, utils.ToPtr("Workspace hibernation failed. Check events for more information.")); err != nil {
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
	if workspace.Status.HibernatedAt != nil && utils.NotNilAndNot(workspace.Spec.Hibernated, true) {
		err := r.resumeModules(ctx, workspace)
		if err != nil {
			if err := r.setPhaseFailed(ctx, workspace, utils.ToPtr("Workspace resume failed. Check events for more information.")); err != nil {
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

func (r *WorkspaceReconciler) handleAutoHibernation(ctx context.Context, workspace *batchv1.Workspace) ctrl.Result { //nolint:unparam
	log := logf.FromContext(ctx)

	if workspace.Spec.AutoHibernation != nil {
		if workspace.Spec.AutoHibernation.Enabled {
			if err := r.addSleepCron(log, workspace, true); err != nil {
				log.Error(err, "Failed to add sleep cron schedule for workspace", "namespace", workspace.Namespace, "name", workspace.Name)
				r.Recorder.Event(workspace, "Warning", "HibernationCron", fmt.Sprintf("Failed to add auto-hibernation schedule: %v", err))
			}

			if workspace.Spec.AutoHibernation.WakeSchedule != nil {
				if err := r.addResumeCron(log, workspace, true); err != nil {
					log.Error(err, "Failed to add resume cron schedule for workspace", "namespace", workspace.Namespace, "name", workspace.Name)
					r.Recorder.Event(workspace, "Warning", "HibernationCron", fmt.Sprintf("Failed to add auto-resume schedule: %v", err))
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

func (r *WorkspaceReconciler) forkWorkspace(
	ctx context.Context,
	sourceWorkspace, destWorkspace *batchv1.Workspace,
) error {
	log := logf.FromContext(ctx)

	modules, err := r.getRelatedModules(ctx, sourceWorkspace.Namespace, sourceWorkspace.Name)
	if err != nil {
		return fmt.Errorf("failed to get related modules for source workspace %s/%s: %w", sourceWorkspace.Namespace, sourceWorkspace.Name, err)
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
				log.Error(err, "failed to create module from source workspace", "module_name", module.Name, "module_namespace", module.Namespace)
				return
			}

			time.Sleep(time.Millisecond * 500)

		waitForInitialNewModuleState:
			for range 180 {
				if err := r.Get(ctx, client.ObjectKeyFromObject(newModule), newModule); err != nil {
					log.Error(err, "failed to get new module during initial creation wait", "module_name", newModule.Name, "module_namespace", newModule.Namespace)
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
					log.Error(errors.New("unexpected new module phase encountered"), "encountered unexpected new module phase during initial creation wait", "module_name", newModule.Name, "module_namespace", newModule.Namespace, "phase", newModule.Status.Phase)
					return
				}
			}

			switch newModule.Status.Phase {
			case batchv1.ModulePhaseReady, batchv1.ModulePhaseSleeped:
			default:
				log.Error(errors.New("new module did not become ready or hibernated within the expected timeout"), "timed out waiting for new module to become ready or hibernated", "module_name", newModule.Name, "module_namespace", newModule.Namespace, "final_phase", newModule.Status.Phase)
				return
			}

			if err := r.migrateModuleData(ctx, &module, newModule, sourceWorkspace, destWorkspace); err != nil {
				log.Error(err, "failed to migrate module data", "module_name", module.Name, "module_namespace", module.Namespace)
				r.Recorder.Event(destWorkspace, "Warning", "DataMigration", fmt.Sprintf("Failed to migrate data for new module %s/%s (from source %s/%s): %v", newModule.Namespace, newModule.Name, module.Namespace, module.Name, err))
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

	// This block checks if both source and destination modules are Helm modules.
	// It returns early if the resource annotation is missing or if it's a CustomModule, as data migration is only supported for Helm modules.
	{
		resourceAnnotation := sourceModule.Annotations[types.ModuleAnnotationKeys.Resource]
		if resourceAnnotation == "" {
			return nil
		}

		if err := resources.HandleResource([]byte(resourceAnnotation), nil,
			func(helmModule resources.HelmModule) error { return nil },
			func(_ resources.CustomModule) error {
				return errors.New("not supported")
			},
		); err != nil {
			return nil
		}

		resourceAnnotation = destModule.Annotations[types.ModuleAnnotationKeys.Resource]
		if resourceAnnotation == "" {
			return nil
		}

		if err := resources.HandleResource([]byte(resourceAnnotation), nil,
			func(helmModule resources.HelmModule) error { return nil },
			func(_ resources.CustomModule) error {
				return errors.New("not supported")
			},
		); err != nil {
			return nil
		}
	}

	if destModule.Spec.Hibernated == nil || utils.NotNilAndNot(destModule.Spec.Hibernated, true) {
		patch := client.MergeFrom(destModule.DeepCopy())
		destModule.Spec.Hibernated = utils.ToPtr(true)
		if err := r.Patch(ctx, destModule, patch); err != nil {
			return err
		}
	}

	defer func() {
		patch := client.MergeFrom(destModule.DeepCopy())
		destModule.Spec.Hibernated = utils.ToPtr(false)
		if err := r.Patch(ctx, destModule, patch); err != nil {
			log.Error(err, "failed to update destination module to hibernate state")
		}
	}()

waitForSleepedDestModuleState:
	for range 180 {
		if err := r.Get(ctx, client.ObjectKeyFromObject(destModule), destModule); err != nil {
			log.Error(err, "failed to get destination module during hibernation wait", "module_name", destModule.Name, "module_namespace", destModule.Namespace)
			time.Sleep(time.Second * 3) // Add a sleep here to prevent busy-looping on Get errors
			continue
		}

		switch destModule.Status.Phase {
		case batchv1.ModulePhaseSleeped:
			break waitForSleepedDestModuleState
		case "", batchv1.ModulePhaseReady, batchv1.ModulePhaseInstalling, batchv1.ModulePhaseSleeping, batchv1.ModulePhaseResuming:
			time.Sleep(time.Second * 3)
			continue
		case batchv1.ModulePhaseUninstalling, batchv1.ModulePhaseFailed:
			return nil // Return nil as migration cannot proceed if module is uninstalling or failed
		default:
			return fmt.Errorf("unexpected destination module phase '%s' encountered during hibernation wait for module %s/%s", destModule.Status.Phase, destModule.Namespace, destModule.Name)
		}
	}

	if destModule.Status.Phase != batchv1.ModulePhaseSleeped {
		return fmt.Errorf("timed out waiting for destination module %s/%s to become hibernated, current phase: %s", destModule.Namespace, destModule.Name, destModule.Status.Phase)
	}

	sourceModuleOldHibernationStatus := true
	if sourceModule.Spec.Hibernated == nil || utils.NotNilAndNot(sourceModule.Spec.Hibernated, true) {
		patch := client.MergeFrom(sourceModule.DeepCopy())
		sourceModule.Spec.Hibernated = utils.ToPtr(true)
		sourceModuleOldHibernationStatus = false
		if err := r.Patch(ctx, sourceModule, patch); err != nil {
			return err
		}
	}

	defer func() {
		if sourceModule.Spec.Hibernated != nil && *sourceModule.Spec.Hibernated != sourceModuleOldHibernationStatus {
			patch := client.MergeFrom(sourceModule.DeepCopy())
			sourceModule.Spec.Hibernated = utils.ToPtr(sourceModuleOldHibernationStatus)
			if err := r.Patch(ctx, sourceModule, patch); err != nil {
				log.Error(err, "failed to update source module to hibernate state")
			}
		}
	}()

waitForSleepedSourceModuleState:
	for range 180 {
		if err := r.Get(ctx, client.ObjectKeyFromObject(sourceModule), sourceModule); err != nil {
			log.Error(err, "failed to get source module during hibernation wait", "module_name", sourceModule.Name, "module_namespace", sourceModule.Namespace)
			time.Sleep(time.Second * 3) // Add a sleep here to prevent busy-looping on Get errors
			continue
		}

		switch sourceModule.Status.Phase {
		case batchv1.ModulePhaseSleeped:
			break waitForSleepedSourceModuleState
		case "", batchv1.ModulePhaseReady, batchv1.ModulePhaseInstalling, batchv1.ModulePhaseSleeping, batchv1.ModulePhaseResuming:
			time.Sleep(time.Second * 3)
			continue
		case batchv1.ModulePhaseUninstalling, batchv1.ModulePhaseFailed:
			return nil // Return nil as migration cannot proceed if module is uninstalling or failed
		default:
			return fmt.Errorf("unexpected source module phase '%s' encountered during hibernation wait for module %s/%s", sourceModule.Status.Phase, sourceModule.Namespace, sourceModule.Name)
		}
	}

	if sourceModule.Status.Phase != batchv1.ModulePhaseSleeped {
		return fmt.Errorf("timed out waiting for source module %s/%s to become hibernated, current phase: %s", sourceModule.Namespace, sourceModule.Name, sourceModule.Status.Phase)
	}

	var sourceHelmModule resources.HelmModule
	{
		resourceAnnotation := sourceModule.Annotations[types.ModuleAnnotationKeys.Resource]
		if resourceAnnotation == "" {
			return fmt.Errorf("module %s/%s is missing resource annotation", sourceModule.Namespace, sourceModule.Name)
		}
		sourceModuleData := []byte(resourceAnnotation)

		managerData, ok := sourceModule.Annotations[types.ModuleAnnotationKeys.ManagerData]

		metaData := make(managerBase.MetaData)
		if ok && managerData != "" {
			err := metaData.Parse([]byte(managerData))
			if err != nil {
				log.Error(err, "failed to parse manager data for module", "module", sourceModule.Name, "namespace", sourceModule.Namespace)
			}
		}

		configMap := make(map[string]any)
		configMapData, ok := sourceModule.Annotations[types.ModuleAnnotationKeys.BaseModuleConfig]
		if ok {
			if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
				log.Error(err, "failed to unmarshal module config from annotation")
			}
		}

		err := resources.HandleResource(sourceModuleData, &configMap,
			func(helmModule resources.HelmModule) error {
				releaseName, ok := metaData[manager.HelmMetaDataKeys.ReleaseName].(string)
				if !ok {
					return fmt.Errorf("source module release name not found in manager metadata or not a string")
				}

				err := helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
				if err != nil {
					return fmt.Errorf("failed to render Helm module spec: %v", err)
				}
				sourceHelmModule = helmModule

				return nil
			},
			nil,
		)
		if err != nil {
			return err
		}
	}

	{
		resourceAnnotation := destModule.Annotations[types.ModuleAnnotationKeys.Resource]
		if resourceAnnotation == "" {
			return fmt.Errorf("module %s/%s is missing resource annotation", destModule.Namespace, destModule.Name)
		}
		destModuleData := []byte(resourceAnnotation)

		managerData, ok := destModule.Annotations[types.ModuleAnnotationKeys.ManagerData]

		metaData := make(managerBase.MetaData)
		if ok && managerData != "" {
			err := metaData.Parse([]byte(managerData))
			if err != nil {
				log.Error(err, "failed to parse manager data for module", "module", destModule.Name, "namespace", destModule.Namespace)
			}
		}

		configMap := make(map[string]any)
		configMapData, ok := destModule.Annotations[types.ModuleAnnotationKeys.BaseModuleConfig]
		if ok {
			if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
				log.Error(err, "failed to unmarshal module config from annotation")
			}
		}

		err := resources.HandleResource(destModuleData, &configMap,
			func(helmModule resources.HelmModule) error {
				releaseName, ok := metaData[manager.HelmMetaDataKeys.ReleaseName].(string)
				if !ok {
					return fmt.Errorf("destination module release name not found in manager metadata or not a string")
				}

				err := helmModule.RenderSpec(helmModule.NewRenderData(configMap, releaseName))
				if err != nil {
					return fmt.Errorf("failed to render Helm module spec: %v", err)
				}

				pvMigrateService := services.NewPVMigrateService(nil)
				destKubeConfig, err := NewKubernetesConfig(ctx, destWorkspace.Spec.Connection, r.Client)
				if err != nil {
					return err
				}

				sourceKubeConfig, err := NewKubernetesConfig(ctx, sourceWorkspace.Spec.Connection, r.Client)
				if err != nil {
					return err
				}

				// Convert rest.Config to kubeconfig format and write to files
				destKubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("dest-kubeconfig-%s-*.yaml", destWorkspace.Name))
				destKubeconfigData, err := clientcmd.Write(*ConvertRestConfigToAPIConfig(destKubeConfig, "", "", ""))
				if err != nil {
					return err
				}
				if err := os.WriteFile(destKubeconfigPath, destKubeconfigData, 0600); err != nil {
					return err
				}

				sourceKubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("source-kubeconfig-%s-*.yaml", sourceWorkspace.Name))
				sourceKubeconfigData, err := clientcmd.Write(*ConvertRestConfigToAPIConfig(sourceKubeConfig, "", "", ""))
				if err != nil {
					return err
				}
				if err := os.WriteFile(sourceKubeconfigPath, sourceKubeconfigData, 0600); err != nil {
					return err
				}

				if sourceHelmModule.Spec.Migration != nil &&
					sourceHelmModule.Spec.Migration.PVC != nil &&
					sourceHelmModule.Spec.Migration.PVC.Enabled {
					for i, sourcePVCName := range sourceHelmModule.Spec.Migration.PVC.Names {
						if err := pvMigrateService.MigratePVC(ctx,
							sourceKubeconfigPath, sourcePVCName, sourceHelmModule.Spec.Namespace,
							destKubeconfigPath, helmModule.Spec.Migration.PVC.Names[i], helmModule.Spec.Namespace,
						); err != nil {
							log.Error(err, "failed to migrate PVC",
								"sourcePVC", sourcePVCName,
								"sourceNamespace", sourceHelmModule.Spec.Namespace,
								"destinationPVC", helmModule.Spec.Migration.PVC.Names[i],
								"destinationNamespace", helmModule.Spec.Namespace)
						}
					}
				}

				if err = os.Remove(destKubeconfigPath); err != nil {
					log.Error(err, "failed to remove temp destination kubeconfig file", "path", destKubeconfigPath)
				}
				if err = os.Remove(sourceKubeconfigPath); err != nil {
					log.Error(err, "failed to remove temp source kubeconfig file", "path", sourceKubeconfigPath)
				}

				return nil
			},
			nil,
		)
		if err != nil {
			return err
		}
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
		if module.Spec.Hibernated == nil || utils.NotNilAndNot(module.Spec.Hibernated, true) {
			patch := client.MergeFrom(module.DeepCopy())
			module.Spec.Hibernated = utils.ToPtr(true)
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
		if module.Spec.Hibernated == nil || utils.NotNilAndZero(module.Spec.Hibernated) {
			patch := client.MergeFrom(module.DeepCopy())
			module.Spec.Hibernated = utils.ToPtr(false)
			if err := r.Patch(ctx, &module, patch); err != nil {
				log.Error(err, "failed to update module to hibernate state")
			}
		}
	}

	return nil
}

func (r *WorkspaceReconciler) getRelatedModules(ctx context.Context, workspaceNamespace, workspaceName string) (*batchv1.ModuleList, error) {
	modules := &batchv1.ModuleList{}
	if err := r.List(ctx, modules, client.MatchingFields{"spec.workspace.namespace": workspaceNamespace, "spec.workspace.name": workspaceName}); err != nil {
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

func (r *WorkspaceReconciler) setPhaseHibernated(ctx context.Context, workspace *batchv1.Workspace, message *string) error {
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
