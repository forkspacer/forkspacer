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
	"net/url"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	validationutils "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
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

// +kubebuilder:webhook:path=/mutate-batch-environment-sh-v1-module,mutating=true,failurePolicy=fail,sideEffects=None,groups=batch.environment.sh,resources=modules,verbs=create;update,versions=v1,name=mmodule-v1.kb.io,admissionReviewVersions=v1

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

	// TODO(user): fill in your defaulting logic.

	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-batch-environment-sh-v1-module,mutating=false,failurePolicy=fail,sideEffects=None,groups=batch.environment.sh,resources=modules,verbs=create;update,versions=v1,name=vmodule-v1.kb.io,admissionReviewVersions=v1

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

	return nil, validateModule(ctx, v.Client, module)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Module.
func (v *ModuleCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	module, ok := newObj.(*batchv1.Module)
	if !ok {
		return nil, fmt.Errorf("expected a Module object for the newObj but got %T", newObj)
	}
	modulelog.Info("Validation for Module upon update", "name", module.GetName())

	return nil, validateModule(ctx, v.Client, module)
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

func validateModule(ctx context.Context, c client.Client, module *batchv1.Module) error {
	var allErrs field.ErrorList
	if err := validateModuleName(module); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := validateModuleSpec(ctx, c, module); err != nil {
		allErrs = append(allErrs, err...)
	}
	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: "batch.environment.sh", Kind: "Module"},
		module.Name, allErrs,
	)
}

func validateModuleName(module *batchv1.Module) *field.Error {
	if len(module.Name) > validationutils.DNS1035LabelMaxLength-11 {
		return field.Invalid(field.NewPath("metadata").Child("name"), module.Name, "must be no more than 52 characters")
	}
	return nil
}

func validateModuleSpec(ctx context.Context, c client.Client, module *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	if err := validateModuleSource(module.Spec.Source, field.NewPath("spec").Child("source")); err != nil {
		allErrs = append(allErrs, err)
	}

	if errs := validateModuleWorkspace(ctx, c, module); errs != nil {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func validateModuleSource(moduleSource batchv1.ModuleSource, fldPath *field.Path) *field.Error {
	if moduleSource.Raw != nil {
		return nil
	} else if moduleSource.HttpURL != nil {
		moduleURLParsed, err := url.Parse(*moduleSource.HttpURL)
		if err != nil {
			return field.Invalid(fldPath, *moduleSource.HttpURL, err.Error())
		}

		switch moduleURLParsed.Scheme {
		case "http", "https":
		default:
			return field.Invalid(fldPath.Child("httpURL"), *moduleSource.HttpURL, fmt.Sprintf("unsupported Http URL scheme, got '%s'", moduleURLParsed.Scheme))
		}

		return nil
	} else if moduleSource.Github != nil {
		return field.Invalid(fldPath.Child("github"), "", "'github' module source type is not yet supported")
	} else {
		return field.Invalid(fldPath, moduleSource, "exactly one of 'raw', 'httpURL', or 'github' must be specified")
	}
}

func validateModuleWorkspace(ctx context.Context, c client.Client, module *batchv1.Module) field.ErrorList {
	var allErrs field.ErrorList

	if module.Spec.Workspace.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("workspace").Child("name"),
			"workspace name is required",
		))
		return allErrs
	}

	if module.Spec.Workspace.Namespace == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("workspace").Child("namespace"),
			"workspace namespace is required",
		))
		return allErrs
	}

	workspace := &batchv1.Workspace{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      module.Spec.Workspace.Name,
		Namespace: module.Spec.Workspace.Namespace,
	}, workspace)

	if err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec").Child("workspace").Child("name"),
				workspace.Name,
				fmt.Sprintf("workspace %s/%s not found", module.Spec.Workspace.Namespace, module.Spec.Workspace.Name),
			))
		} else {
			allErrs = append(allErrs, field.InternalError(
				field.NewPath("spec").Child("workspace").Child("name"),
				fmt.Errorf("failed to validate workspace reference: %v", err),
			))
		}
		return allErrs
	}

	return allErrs
}
