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

package v1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sTypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/google/go-cmp/cmp"
)

// nolint:unused
// log is for logging in this package.
var modulelog = logf.Log.WithName("module-resource")

// SetupModuleWebhookWithManager registers the webhook for Module in the manager.
func SetupModuleWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&batchv1.Module{}).
		WithValidator(&ModuleCustomValidator{Client: mgr.GetClient()}).
		WithDefaulter(&ModuleCustomDefaulter{}).
		Complete()
}

//nolint:lll
// +kubebuilder:webhook:path=/mutate-batch-forkspacer-com-v1-module,mutating=true,failurePolicy=fail,sideEffects=None,groups=batch.forkspacer.com,resources=modules,verbs=create;update,versions=v1,name=mmodule-v1.kb.io,admissionReviewVersions=v1

// ModuleCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Module when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type ModuleCustomDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

var _ webhook.CustomDefaulter = &ModuleCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Module.
func (d *ModuleCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	module, ok := obj.(*batchv1.Module)

	if !ok {
		return fmt.Errorf("expected an Module object but got %T", obj)
	}
	modulelog.Info("Defaulting for Module", "name", module.GetName())

	// Generate and persist the Helm release name if this is a Helm module
	if module.Spec.Helm != nil {
		if _, err := module.NewHelmReleaseName(); err != nil {
			return fmt.Errorf("failed to generate Helm release name: %w", err)
		}

		// Set ExistingRelease namespace to match Helm namespace if adopting an existing release
		if module.Spec.Helm.ExistingRelease != nil {
			module.Spec.Helm.Namespace = module.Spec.Helm.ExistingRelease.Namespace
		}
	}

	return nil
}

//nolint:lll
// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-batch-forkspacer-com-v1-module,mutating=false,failurePolicy=fail,sideEffects=None,groups=batch.forkspacer.com,resources=modules,verbs=create;update,versions=v1,name=vmodule-v1.kb.io,admissionReviewVersions=v1

// ModuleCustomValidator struct is responsible for validating the Module resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ModuleCustomValidator struct {
	Client client.Client
}

var _ webhook.CustomValidator = &ModuleCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Module.
func (v *ModuleCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	module, ok := obj.(*batchv1.Module)
	if !ok {
		return nil, fmt.Errorf("expected a Module object but got %T", obj)
	}
	modulelog.Info("Validation for Module upon creation", "name", module.GetName())

	allErrs := validateModule(ctx, v.Client, module)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "batch.forkspacer.com", Kind: "Module"},
			module.Name, allErrs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Module.
func (v *ModuleCustomValidator) ValidateUpdate(
	ctx context.Context,
	oldObj, newObj runtime.Object,
) (admission.Warnings, error) {
	oldModule, ok := oldObj.(*batchv1.Module)
	if !ok {
		return nil, fmt.Errorf("expected a Module object for the oldObj but got %T", oldObj)
	}

	newModule, ok := newObj.(*batchv1.Module)
	if !ok {
		return nil, fmt.Errorf("expected a Module object for the newObj but got %T", newObj)
	}
	modulelog.Info("Validation for Module upon update", "name", newModule.GetName())

	if allErrs := validateModuleImmutableUpdateFields(oldModule, newModule); len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "batch.forkspacer.com", Kind: "Module"},
			newModule.Name, allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Module.
func (v *ModuleCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	module, ok := obj.(*batchv1.Module)
	if !ok {
		return nil, fmt.Errorf("expected a Module object but got %T", obj)
	}
	modulelog.Info("Validation for Module upon deletion", "name", module.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}

func validateModule(ctx context.Context, c client.Client, module *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	if err := validateModuleSpec(ctx, c, module); err != nil {
		allErrs = append(allErrs, err...)
	}

	return allErrs
}

func validateModuleSpec(ctx context.Context, c client.Client, module *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	if errs := validateModuleWorkspace(ctx, c, module); errs != nil {
		allErrs = append(allErrs, errs...)
	}

	if errs := validateModuleHelmChart(module); errs != nil {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func validateModuleWorkspace(ctx context.Context, c client.Client, module *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	workspace := &batchv1.Workspace{}
	err := c.Get(ctx, k8sTypes.NamespacedName{
		Name:      module.Spec.Workspace.Name,
		Namespace: module.Spec.Workspace.Namespace,
	}, workspace)

	if err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec").Child("workspace"),
				module.Spec.Workspace,
				fmt.Sprintf("workspace %s/%s not found", module.Spec.Workspace.Namespace, module.Spec.Workspace.Name),
			))
		} else {
			allErrs = append(allErrs, field.InternalError(
				field.NewPath("spec").Child("workspace"),
				fmt.Errorf("failed to validate workspace reference: %v", err),
			))
		}
		return allErrs
	}

	return allErrs
}

func validateModuleHelmChart(module *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	// Only validate if this is a Helm module
	if module.Spec.Helm == nil {
		return allErrs
	}

	chart := &module.Spec.Helm.Chart
	chartPath := field.NewPath("spec").Child("helm").Child("chart")

	// Count how many chart sources are specified
	sourceCount := 0
	if chart.Repo != nil {
		sourceCount++
	}
	if chart.ConfigMap != nil {
		sourceCount++
	}
	if chart.Git != nil {
		sourceCount++
	}

	// Exactly one source must be specified
	if sourceCount == 0 {
		allErrs = append(allErrs, field.Required(
			chartPath,
			"must specify exactly one of: repo, configMap, or git",
		))
	} else if sourceCount > 1 {
		allErrs = append(allErrs, field.Invalid(
			chartPath,
			chart,
			"must specify exactly one of: repo, configMap, or git (multiple sources specified)",
		))
	}

	return allErrs
}

func validateModuleImmutableUpdateFields(oldModule, newModule *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	if oldModule == nil || newModule == nil {
		return allErrs
	}

	if !cmp.Equal(oldModule.Spec.Workspace, newModule.Spec.Workspace) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("workspace"),
				newModule.Spec.Workspace, "field is immutable",
			),
		)
	}

	if !cmp.Equal(oldModule.Spec.Config, newModule.Spec.Config) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("config"),
				newModule.Spec.Config, "field is immutable",
			),
		)
	}

	// =================================== Helm ===================================
	if oldModule.Spec.Helm == nil && newModule.Spec.Helm != nil {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("helm"),
				newModule.Spec.Helm, "field cannot be added once the object is created",
			),
		)
	} else if oldModule.Spec.Helm != nil && newModule.Spec.Helm == nil {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("helm"),
				newModule.Spec.Helm, "field cannot be removed once the object is created",
			),
		)
	} else if oldModule.Spec.Helm != nil && newModule.Spec.Helm != nil {
		if !cmp.Equal(oldModule.Spec.Helm.ExistingRelease, newModule.Spec.Helm.ExistingRelease) {
			allErrs = append(allErrs,
				field.Invalid(
					field.NewPath("spec").Child("helm").Child("existingRelease"),
					newModule.Spec.Helm.ExistingRelease, "field is immutable",
				),
			)
		}

		if !cmp.Equal(oldModule.Spec.Helm.Namespace, newModule.Spec.Helm.Namespace) {
			allErrs = append(allErrs,
				field.Invalid(
					field.NewPath("spec").Child("helm").Child("namespace"),
					newModule.Spec.Helm.Namespace, "field is immutable",
				),
			)
		}

		if !cmp.Equal(oldModule.Spec.Helm.Outputs, newModule.Spec.Helm.Outputs) {
			allErrs = append(allErrs,
				field.Invalid(
					field.NewPath("spec").Child("helm").Child("outputs"),
					newModule.Spec.Helm.Outputs, "field is immutable",
				),
			)
		}
	}

	// =================================== Custom Module ===================================
	if oldModule.Spec.Custom == nil && newModule.Spec.Custom != nil {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("custom"),
				newModule.Spec.Custom, "field cannot be added once the object is created",
			),
		)
	} else if oldModule.Spec.Custom != nil && newModule.Spec.Custom == nil {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("custom"),
				newModule.Spec.Custom, "field cannot be removed once the object is created",
			),
		)
	} else if oldModule.Spec.Custom != nil && newModule.Spec.Custom != nil {
		if !cmp.Equal(oldModule.Spec.Custom.Image, newModule.Spec.Custom.Image) {
			allErrs = append(allErrs,
				field.Invalid(
					field.NewPath("spec").Child("custom").Child("image"),
					newModule.Spec.Custom.Image, "field is immutable",
				),
			)
		}
	}

	return allErrs
}
