package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	"github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ base.IManager = ModuleCustomManager{}

type ModuleCustomManager struct {
	controllerClient client.Client
	podResource      *corev1.Pod
}

func NewModuleCustomManager(
	ctx context.Context,
	controllerClient client.Client,
	kubernetesConfig *rest.Config,
	customModule *resources.CustomModule,
	config map[string]any,
	metaData base.MetaData,
) (base.IManager, error) {
	log := logf.FromContext(ctx)

	if err := customModule.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate custom module: %w", err)
	}

	secretName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.SecretName)
	if secretName == "" {
		configJSON, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal custom module configuration: %w", err)
		}

		data := map[string][]byte{
			"config.json": configJSON,
		}

		for _, permission := range customModule.Spec.Permissions {
			switch permission {
			case resources.CustomModulePermissionWorkspace:
				kubeconfig, err := clientcmd.Write(
					*utils.ConvertRestConfigToAPIConfig(kubernetesConfig, "", "", ""),
				)
				if err != nil {
					return nil, fmt.Errorf("failed to write workspace kubeconfig: %w", err)
				}

				data["workspace_kubeconfig"] = kubeconfig
			case resources.CustomModulePermissionController:
				// No specific data needed for controller permission currently
			default:
				log.Error(
					fmt.Errorf("unsupported custom module permission: %s", permission),
					"encountered an unsupported permission during custom module secret generation",
				)
				return nil, err
			}
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "custom-manager-",
				Namespace:    kubernetesCons.OperatorNamespace,
				Labels: map[string]string{
					kubernetesCons.BaseLabelKey: "true",
				},
			},
			Immutable: utils.ToPtr(false),
			Data:      data,
			Type:      corev1.SecretTypeOpaque, // or other types like 'kubernetes.io/dockerconfigjson'
		}
		if err := controllerClient.Create(ctx, secret); err != nil {
			return nil, fmt.Errorf("failed to create custom module secret: %w", err)
		}

		secretName = secret.Name
		metaData[managerCons.CustomMetaDataKeys.SecretName] = secretName
	}

	imagePullSecrets := make([]corev1.LocalObjectReference, len(customModule.Spec.ImagePullSecrets))
	for i, imagePullSecret := range customModule.Spec.ImagePullSecrets {
		imagePullSecrets[i] = corev1.LocalObjectReference{
			Name: imagePullSecret,
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "custom-manager-",
			Namespace:    kubernetesCons.OperatorNamespace,
			Labels: map[string]string{
				kubernetesCons.BaseLabelKey: "true",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:    corev1.RestartPolicyNever,
			ImagePullSecrets: imagePullSecrets,
			Containers: []corev1.Container{
				{
					Name:  "worker",
					Image: customModule.Spec.Image,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "secret-volume",
							MountPath: "/forkspacer",
							ReadOnly:  false,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "secret-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName,
						},
					},
				},
			},
		},
	}

	if slices.Contains(customModule.Spec.Permissions, resources.CustomModulePermissionController) {
		pod.Spec.ServiceAccountName = "forkspacer-controller-manager"
	}

	return &ModuleCustomManager{controllerClient, pod}, nil
}

func (m ModuleCustomManager) Install(ctx context.Context, metaData base.MetaData) error {
	// TODO: implement
	return nil
}

func (m ModuleCustomManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	// TODO: implement
	return nil
}

func (m ModuleCustomManager) Sleep(ctx context.Context, metaData base.MetaData) error {
	// TODO: implement
	return nil
}

func (m ModuleCustomManager) Resume(ctx context.Context, metaData base.MetaData) error {
	// TODO: implement
	return nil
}
