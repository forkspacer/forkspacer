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

	cronCons "github.com/forkspacer/forkspacer/pkg/constants/cron"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sTypes "k8s.io/apimachinery/pkg/types"
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

	// Setup connection for managed workspaces
	if workspace.Spec.Type == batchv1.WorkspaceTypeManaged {
		// Generate dedicated namespace and secret name for this managed workspace
		vclusterNamespace := fmt.Sprintf("forkspacer-%s", workspace.Name)
		secretNamePrefix := "vc-kubeconfig"
		secretName := fmt.Sprintf("%s-%s", secretNamePrefix, workspace.Name)

		// Set connection to use kubeconfig from the vcluster secret in the dedicated namespace
		workspace.Spec.Connection = batchv1.WorkspaceConnection{
			Type: batchv1.WorkspaceConnectionTypeKubeconfig,
			SecretReference: &batchv1.WorkspaceConnectionSecretReference{
				Name:      secretName,
				Namespace: vclusterNamespace,
				Key:       "config",
			},
		}
	}

	if workspace.Spec.From != nil {
		fromWorkspace := &batchv1.Workspace{}
		if err := d.Get(ctx,
			k8sTypes.NamespacedName{
				Name:      workspace.Spec.From.Name,
				Namespace: workspace.Spec.From.Namespace,
			}, fromWorkspace,
		); err == nil {
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
	oldWorkspace, ok := oldObj.(*batchv1.Workspace)
	if !ok {
		return nil, fmt.Errorf("expected a Workspace object for the oldObj but got %T", oldObj)
	}

	newWorkspace, ok := newObj.(*batchv1.Workspace)
	if !ok {
		return nil, fmt.Errorf("expected a Workspace object for the newObj but got %T", newObj)
	}
	workspacelog.Info("Validation for Workspace upon update", "name", newWorkspace.GetName())

	if allErrs := validateWorkspaceImmutableUpdateFields(oldWorkspace, newWorkspace); len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "batch.forkspacer.com", Kind: "Workspace"},
			newWorkspace.Name, allErrs,
		)
	}

	allErrs := validateWorkspace(ctx, v.Client, newWorkspace)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "batch.forkspacer.com", Kind: "Workspace"},
			newWorkspace.Name, allErrs,
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

	if err := validateWorkspaceSpec(ctx, c, workspace); err != nil {
		allErrs = append(allErrs, err...)
	}

	return allErrs
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

	// Skip secret validation for managed workspaces - the kubeconfig secret
	// is created during vcluster installation by the controller
	if workspace.Spec.Type != batchv1.WorkspaceTypeManaged {
		if errs := validateWorkspaceSecretReference(ctx, c, workspace.Spec.Connection); errs != nil {
			allErrs = append(allErrs, errs...)
		}
	}

	return allErrs
}

func validateWorkspaceScheduleFormat(schedule string, fldPath *field.Path) *field.Error {
	parser := cron.NewParser(cronCons.CronParserOptions)

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

	fromWorkspace := &batchv1.Workspace{}
	err := c.Get(ctx, k8sTypes.NamespacedName{
		Name:      fromReference.Name,
		Namespace: fromReference.Namespace,
	}, fromWorkspace)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return field.Invalid(
				field.NewPath("spec").Child("from"),
				fromReference,
				fmt.Sprintf("workspace %s/%s not found", fromReference.Namespace, fromReference.Name),
			)
		} else {
			return field.InternalError(
				field.NewPath("spec").Child("from"),
				fmt.Errorf("failed to validate from reference: %v", err),
			)
		}
	}

	return nil
}

func validateWorkspaceSecretReference(
	ctx context.Context,
	c client.Client,
	connection batchv1.WorkspaceConnection,
) field.ErrorList {
	var allErrs field.ErrorList

	if connection.Type != batchv1.WorkspaceConnectionTypeKubeconfig {
		return nil
	}

	if connection.SecretReference == nil {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			"secretReference is required when connection type is kubeconfig",
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
				field.NewPath("spec").Child("connection").Child("secretReference"),
				connection.SecretReference,
				fmt.Sprintf("secret %s/%s not found", connection.SecretReference.Namespace, connection.SecretReference.Name),
			))
		} else {
			allErrs = append(allErrs, field.InternalError(
				field.NewPath("spec").Child("connection").Child("secretReference"),
				fmt.Errorf("failed to validate secret reference: %v", err),
			))
		}
		return allErrs
	}

	if errs := validateWorkspaceKubeconfigInSecret(
		secret,
		*connection.SecretReference,
	); errs != nil {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func validateWorkspaceKubeconfigInSecret(
	secret *corev1.Secret,
	secretReference batchv1.WorkspaceConnectionSecretReference,
) field.ErrorList {
	var allErrs field.ErrorList

	kubeconfigData, exists := secret.Data[secretReference.Key]
	if !exists {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			fmt.Sprintf("secret %s/%s must contain a %q field",
				secretReference.Namespace, secretReference.Name, secretReference.Key),
		))
		return allErrs
	}

	if len(kubeconfigData) == 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			secretReference,
			fmt.Sprintf("%q field in secret %s/%s cannot be empty",
				secretReference.Key, secretReference.Namespace, secretReference.Name),
		))
		return allErrs
	}

	_, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec").Child("connection").Child("secretReference"),
			secretReference,
			fmt.Sprintf(
				"%q field in secret %s/%s is not a valid kubeconfig: %v",
				secretReference.Key, secretReference.Namespace, secretReference.Name, err,
			),
		))
	}

	return allErrs
}

func validateWorkspaceImmutableUpdateFields(oldWorkspace, newWorkspace *batchv1.Workspace) field.ErrorList {
	var allErrs field.ErrorList

	if oldWorkspace == nil || newWorkspace == nil {
		return allErrs
	}

	if !cmp.Equal(oldWorkspace.Spec.Type, newWorkspace.Spec.Type) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("type"),
				newWorkspace.Spec.Type, "field is immutable",
			),
		)
	}

	if !cmp.Equal(oldWorkspace.Spec.From, newWorkspace.Spec.From) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("from"),
				newWorkspace.Spec.From, "field is immutable",
			),
		)
	}

	if !cmp.Equal(oldWorkspace.Spec.Connection, newWorkspace.Spec.Connection) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec").Child("connection"),
				newWorkspace.Spec.Connection, "field is immutable",
			),
		)
	}

	return allErrs
}
