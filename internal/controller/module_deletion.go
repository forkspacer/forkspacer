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
	"fmt"
	"time"

	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
)

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

	// Attempt uninstallation but always proceed with finalizer removal
	// even if uninstall fails to prevent stuck resources
	if err := r.uninstallModule(ctx, module); err != nil {
		log.Error(err, "module uninstallation failed, proceeding with finalizer removal to allow deletion")
		r.Recorder.Event(module, "Warning", "UninstallError",
			fmt.Sprintf("Module uninstallation failed: %v. Finalizer will be removed to allow deletion.", err))
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
