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
	"time"

	"github.com/forkspacer/forkspacer/pkg/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sTypes "k8s.io/apimachinery/pkg/types"
	validationutils "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/robfig/cron/v3"
)

// nolint:unused
// log is for logging in this package.
var workspacelog = logf.Log.WithName("workspace-resource")

// SetupWorkspaceWebhookWithManager registers the webhook for Workspace in the manager.
func SetupWorkspaceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&batchv1.Workspace{}).
		WithValidator(&WorkspaceCustomValidator{Client: mgr.GetClient()}).
		WithDefaulter(&WorkspaceCustomDefaulter{Client: mgr.GetClient()}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//nolint:lll
// +kubebuilder:webhook:path=/mutate-batch-forkspacer-com-v1-workspace,mutating=true,failurePolicy=fail,sideEffects=None,groups=batch.forkspacer.com,resources=workspaces,verbs=create;update,versions=v1,name=mworkspace-v1.kb.io,admissionReviewVersions=v1

// WorkspaceCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Workspace when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type WorkspaceCustomDefaulter struct {
	client.Client
}

var _ webhook.CustomDefaulter = &WorkspaceCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Workspace.
func (d *WorkspaceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	workspace, ok := obj.(*batchv1.Workspace)

	if !ok {
		return fmt.Errorf("expected an Workspace object but got %T", obj)
	}
	workspacelog.Info("Defaulting for Workspace", "name", workspace.GetName())

	if workspace.Status.LastActivity == nil {
		workspace.Status.LastActivity = &metav1.Time{Time: time.Now()}
	}

	if workspace.Spec.From != nil {
		fromWorkspace := &batchv1.Workspace{}
		if err := d.Get(ctx,
			k8sTypes.NamespacedName{
				Name:      workspace.Spec.From.Name,
				Namespace: workspace.Spec.From.Namespace,
			}, fromWorkspace,
		); err == nil {
			// if workspace.Spec.Hibernated == nil {
			// 	workspace.Spec.Hibernated = fromWorkspace.Spec.Hibernated
			// }

			if workspace.Spec.Connection == nil {
				workspace.Spec.Connection = fromWorkspace.Spec.Connection
			}

			if workspace.Spec.AutoHibernation == nil {
				workspace.Spec.AutoHibernation = fromWorkspace.Spec.AutoHibernation
			}
		}
	}

	return nil
}

//nolint:lll
// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-batch-forkspacer-com-v1-workspace,mutating=false,failurePolicy=fail,sideEffects=None,groups=batch.forkspacer.com,resources=workspaces,verbs=create;update,versions=v1,name=vworkspace-v1.kb.io,admissionReviewVersions=v1
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list

// WorkspaceCustomValidator struct is responsible for validating the Workspace resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type WorkspaceCustomValidator struct {
	client.Client
}

var _ webhook.CustomValidator = &WorkspaceCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Workspace.
func (v *WorkspaceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	workspace, ok := obj.(*batchv1.Workspace)
	if !ok {
		return nil, fmt.Errorf("expected a Workspace object but got %T", obj)
	}
	workspacelog.Info("Validation for Workspace upon creation", "name", workspace.GetName())

	allErrs := validateWorkspace(ctx, v.Client, workspace)

	if err := validateWorkspaceFromReference(ctx, v.Client, workspace.Spec.From); err != nil {
		allErrs = append(allErrs, err)
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "batch.forkspacer.com", Kind: "Workspace"},
			workspace.Name, allErrs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Workspace.
func (v *WorkspaceCustomValidator) ValidateUpdate(
	ctx context.Context,
	oldObj, newObj runtime.Object,
) (admission.Warnings, error) {
	workspace, ok := newObj.(*batchv1.Workspace)
	if !ok {
		return nil, fmt.Errorf("expected a Workspace object for the newObj but got %T", newObj)
	}
	workspacelog.Info("Validation for Workspace upon update", "name", workspace.GetName())

	allErrs := validateWorkspace(ctx, v.Client, workspace)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "batch.forkspacer.com", Kind: "Workspace"},
			workspace.Name, allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Workspace.
func (v *WorkspaceCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	workspace, ok := obj.(*batchv1.Workspace)
	if !ok {
		return nil, fmt.Errorf("expected a Workspace object but got %T", obj)
	}
	workspacelog.Info("Validation for Workspace upon deletion", "name", workspace.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}

func validateWorkspace(ctx context.Context, c client.Client, workspace *batchv1.Workspace) field.ErrorList {
	var allErrs field.ErrorList

	if err := validateWorkspaceName(workspace); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := validateWorkspaceSpec(ctx, c, workspace); err != nil {
		allErrs = append(allErrs, err...)
	}
	if len(allErrs) == 0 {
		return nil
	}

	return allErrs
}

func validateWorkspaceName(workspace *batchv1.Workspace) *field.Error {
	if len(workspace.Name) > validationutils.DNS1035LabelMaxLength-11 {
		return field.Invalid(field.NewPath("metadata").Child("name"), workspace.Name, "must be no more than 52 characters")
	}
	return nil
}

func validateWorkspaceSpec(ctx context.Context, c client.Client, workspace *batchv1.Workspace) field.ErrorList {
	var allErrs field.ErrorList

	if workspace.Spec.AutoHibernation != nil {
		if err := validateWorkspaceScheduleFormat(
			workspace.Spec.AutoHibernation.Schedule,
			field.NewPath("spec").Child("schedule"),
		); err != nil {
			allErrs = append(allErrs, err)
		}

		if workspace.Spec.AutoHibernation.WakeSchedule != nil {
			if err := validateWorkspaceScheduleFormat(
				*workspace.Spec.AutoHibernation.WakeSchedule,
				field.NewPath("spec").Child("wakeSchedule"),
			); err != nil {
				allErrs = append(allErrs, err)
			} else if workspace.Spec.AutoHibernation.Schedule == *workspace.Spec.AutoHibernation.WakeSchedule {
				allErrs = append(allErrs,
					field.Invalid(
						field.NewPath("spec").Child("wakeSchedule"),
						*workspace.Spec.AutoHibernation.WakeSchedule, "wakeSchedule cannot be the same as schedule",
					),
				)
			}
		}
	}

	if errs := validateWorkspaceSecretReference(ctx, c, workspace.Spec.Connection); errs != nil {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func validateWorkspaceScheduleFormat(schedule string, fldPath *field.Path) *field.Error {
	parser := cron.NewParser(types.CronParserOptions)

	if _, err := parser.Parse(schedule); err != nil {
		return field.Invalid(fldPath, schedule, err.Error())
	}

	return nil
}

func validateWorkspaceFromReference(
	ctx context.Context,
	c client.Client,
	fromReference *batchv1.WorkspaceFromReference,
) *field.Error {
	if fromReference == nil {
		return nil
	}

	if fromReference.Name == "" {
		return field.Required(
			field.NewPath("spec").Child("from").Child("name"),
			"name is required when from is specified",
		)
	}

	fromWorkspace := &batchv1.Workspace{}
	err := c.Get(ctx, k8sTypes.NamespacedName{
		Name:      fromReference.Name,
		Namespace: fromReference.Namespace,
	}, fromWorkspace)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return field.Invalid(
				field.NewPath("spec").Child("from").Child("name"),
				fromReference.Name,
				fmt.Sprintf("workspace %s/%s not found", fromReference.Namespace, fromReference.Name),
			)
		} else {
			return field.InternalError(
				field.NewPath("spec").Child("from").Child("name"),
				fmt.Errorf("failed to validate from reference: %v", err),
			)
		}
	}

	return nil
}

func validateWorkspaceSecretReference(
	ctx context.Context,
	c client.Client,
	connection *batchv1.WorkspaceConnection,
) field.ErrorList {
	var allErrs field.ErrorList

	if connection == nil {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection"),
			"connection field is required",
		))
		return allErrs
	}

	if connection.Type != batchv1.WorkspaceConnectionTypeKubeconfig {
		return nil
	}

	if connection.SecretReference == nil {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			"secretReference is required when connection type is Kubeconfig",
		))
		return allErrs
	}

	if connection.SecretReference.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection").Child("secretReference").Child("name"),
			"secret name is required when secretReference is specified",
		))
		return allErrs
	}

	if connection.SecretReference.Namespace == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection").Child("secretReference").Child("namespace"),
			"secret namespace is required when secretReference is specified",
		))
		return allErrs
	}

	secret := &corev1.Secret{}
	err := c.Get(ctx, k8sTypes.NamespacedName{
		Name:      connection.SecretReference.Name,
		Namespace: connection.SecretReference.Namespace,
	}, secret)

	if err != nil {
		if apierrors.IsNotFound(err) {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec").Child("connection").Child("secretReference").Child("name"),
				connection.SecretReference.Name,
				fmt.Sprintf("secret %s/%s not found", connection.SecretReference.Namespace, connection.SecretReference.Name),
			))
		} else {
			allErrs = append(allErrs, field.InternalError(
				field.NewPath("spec").Child("connection").Child("secretReference").Child("name"),
				fmt.Errorf("failed to validate secret reference: %v", err),
			))
		}
		return allErrs
	}

	if errs := validateWorkspaceKubeconfigInSecret(
		secret,
		connection.SecretReference.Name,
		connection.SecretReference.Namespace,
	); errs != nil {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func validateWorkspaceKubeconfigInSecret(secret *corev1.Secret, secretName, secretNamespace string) field.ErrorList {
	var allErrs field.ErrorList

	kubeconfigData, exists := secret.Data["kubeconfig"]
	if !exists {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			fmt.Sprintf("secret %s/%s must contain a 'kubeconfig' field", secretNamespace, secretName),
		))
		return allErrs
	}

	if len(kubeconfigData) == 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			secretName,
			fmt.Sprintf("kubeconfig field in secret %s/%s cannot be empty", secretNamespace, secretName),
		))
		return allErrs
	}

	_, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			secretName,
			fmt.Sprintf("kubeconfig field in secret %s/%s is not a valid kubeconfig: %v", secretNamespace, secretName, err),
		))
	}

	return allErrs
}
