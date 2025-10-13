package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	"github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/utils"
	"github.com/go-viper/mapstructure/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ base.IManager = ModuleCustomManager{}

type ModuleCustomManager struct {
	controllerClient client.Client
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

	podName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerPodName)
	podLabel := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerPodUniqueLabel)
	revertFunc := func() {}
	if podName == "" {
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
					kubernetesCons.BaseLabelKey: "custom-manager-runner-secret",
				},
			},
			Immutable: utils.ToPtr(true),
			Data:      data,
			Type:      corev1.SecretTypeOpaque, // or other types like 'kubernetes.io/dockerconfigjson'
		}
		if err := controllerClient.Create(ctx, secret); err != nil {
			return nil, fmt.Errorf("failed to create custom module secret: %w", err)
		}

		defer func() {
			if err := controllerClient.Delete(ctx, secret); err != nil {
				log.Error(err, "")
			}
		}()

		imagePullSecrets := make([]corev1.LocalObjectReference, len(customModule.Spec.ImagePullSecrets))
		for i, imagePullSecret := range customModule.Spec.ImagePullSecrets {
			imagePullSecrets[i] = corev1.LocalObjectReference{
				Name: imagePullSecret,
			}
		}

		podLabel = "custom-manager-runner-" + time.Now().Format("20060102150405")
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "custom-manager-",
				Namespace:    kubernetesCons.OperatorNamespace,
				Labels: map[string]string{
					kubernetesCons.BaseLabelKey: podLabel,
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy:    corev1.RestartPolicyNever,
				ImagePullSecrets: imagePullSecrets,
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								// corev1.ResourceCPU:              resource.MustParse("250m"),
								// corev1.ResourceMemory:           resource.MustParse("128Mi"),
								corev1.ResourceEphemeralStorage: resource.MustParse("500Mi"),
							},
							Limits: corev1.ResourceList{
								// corev1.ResourceCPU:              resource.MustParse("500m"),
								// corev1.ResourceMemory:           resource.MustParse("256Mi"),
								corev1.ResourceEphemeralStorage: resource.MustParse("5Gi"),
							},
						},
						Name:  "worker",
						Image: customModule.Spec.Image,
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 8080,
							},
						},
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
								SecretName: secret.Name,
							},
						},
					},
				},
			},
		}

		if slices.Contains(customModule.Spec.Permissions, resources.CustomModulePermissionController) {
			pod.Spec.ServiceAccountName = "forkspacer-controller-manager"
		}

		if err = controllerClient.Create(ctx, pod); err != nil {
			return nil, err
		}

		revertFunc = func() {
			if err := controllerClient.Delete(ctx, pod); err != nil {
				log.Error(err, "")
			}
		}

	Loop:
		for {
			if err := controllerClient.Get(ctx, client.ObjectKeyFromObject(pod), pod); err != nil {
				revertFunc()
				return nil, err
			}

			switch pod.Status.Phase {
			case corev1.PodRunning:
				break Loop
			case "", corev1.PodPending:
				time.Sleep(time.Second * 2)
			case corev1.PodSucceeded:
				// If the pod succeeded, it means the custom module runner completed its initial setup and exited.
				// This is unexpected if the intention is to create a service that connects to a running pod.
				return nil, fmt.Errorf("custom module runner pod succeeded unexpectedly: pod phase %s", pod.Status.Phase)
			case corev1.PodFailed:
				return nil, fmt.Errorf("custom module runner pod failed: pod phase %s", pod.Status.Phase)
			default:
				// This covers any other unexpected phases like "Unknown" if it ever occurs
				return nil, fmt.Errorf("custom module runner pod entered an unexpected phase: %s", pod.Status.Phase)
			}
		}

		metaData[managerCons.CustomMetaDataKeys.RunnerPodName] = pod.Name
		metaData[managerCons.CustomMetaDataKeys.RunnerPodUniqueLabel] = podLabel
	}

	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "custom-manager-",
				Namespace:    kubernetesCons.OperatorNamespace,
				Labels: map[string]string{
					kubernetesCons.BaseLabelKey: "custom-manager-service",
				},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					kubernetesCons.BaseLabelKey: podLabel,
				},
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       80,
						TargetPort: intstr.FromInt(8080),
					},
				},
			},
		}

		if err := controllerClient.Create(ctx, service); err != nil {
			revertFunc()
			return nil, err
		}

		metaData[managerCons.CustomMetaDataKeys.RunnerServiceName] = service.Name
	}

	return &ModuleCustomManager{controllerClient}, nil
}

func (m ModuleCustomManager) Install(ctx context.Context, metaData base.MetaData) error {
	log := logf.FromContext(ctx)

	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		log.Error(
			errors.New("runner service name is missing in metadata"),
			"failed to retrieve runner service name required for uninstallation",
		)
	}

	decodedMap := make(map[string]any)
	if err := mapstructure.Decode(metaData[managerCons.CustomMetaDataKeys.InnerMetaData], &decodedMap); err != nil {
		log.Error(err, "")
		// If decoding fails, decodedMap will remain an empty map, which will
		// then be marshaled into an empty JSON object `{}`.
	}

	jsonBytes, err := json.Marshal(decodedMap)
	if err != nil {
		log.Error(err, "failed to marshal decoded InnerMetaData to JSON")
		// Fallback to an empty JSON object if marshaling fails
		jsonBytes = []byte("{}")
	}

	resp, err := http.Post(
		fmt.Sprintf("http://%s.%s.svc.cluster.local:80/install", serviceName, kubernetesCons.OperatorNamespace),
		"application/json",
		bytes.NewBuffer(jsonBytes),
	)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return fmt.Errorf(
			"failed to install custom module %s: received unexpected status code %d, expected 204",
			serviceName, resp.StatusCode,
		)
	}

	// Request was successful (status code 204). Now, get the response body as per the prompt.
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body for custom module install: %w", err)
	}

	// Only attempt to unmarshal if there is content in the response body.
	if len(responseBody) > 0 {
		var installResponse map[string]any
		if err := json.Unmarshal(responseBody, &installResponse); err != nil {
			log.Error(err, "failed to unmarshal install response body", "response", string(responseBody))
			// If unmarshaling fails for a successful request, it's an error.
			return fmt.Errorf("failed to unmarshal install response body after successful request: %w", err)
		}

		// Set the converted map to the metaData with the specified key.
		metaData[managerCons.CustomMetaDataKeys.InnerMetaData] = installResponse
	}

	return nil
}

func (m ModuleCustomManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	log := logf.FromContext(ctx)

	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		log.Error(
			errors.New("runner service name is missing in metadata"),
			"failed to retrieve runner service name required for uninstallation",
		)
	}

	decodedMap := make(map[string]any)
	if err := mapstructure.Decode(metaData[managerCons.CustomMetaDataKeys.InnerMetaData], &decodedMap); err != nil {
		log.Error(err, "")
		// If decoding fails, decodedMap will remain an empty map, which will
		// then be marshaled into an empty JSON object `{}`.
	}

	jsonBytes, err := json.Marshal(decodedMap)
	if err != nil {
		log.Error(err, "failed to marshal decoded InnerMetaData to JSON")
		// Fallback to an empty JSON object if marshaling fails
		jsonBytes = []byte("{}")
	}

	resp, err := http.Post(
		fmt.Sprintf("http://%s.%s.svc.cluster.local:80/uninstall", serviceName, kubernetesCons.OperatorNamespace),
		"application/json",
		bytes.NewBuffer(jsonBytes),
	)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return fmt.Errorf(
			"failed to uninstall custom module %s: received unexpected status code %d, expected 204",
			serviceName, resp.StatusCode,
		)
	}

	service := &corev1.Service{}
	err = m.controllerClient.Get(ctx,
		client.ObjectKey{
			Name:      serviceName,
			Namespace: kubernetesCons.OperatorNamespace,
		},
		service,
	)
	if err != nil {
		log.Error(err, "failed to get custom module service for uninstallation", "serviceName", serviceName)
	}

	if err = m.controllerClient.Delete(ctx, service); err != nil {
		return fmt.Errorf("failed to delete custom module service %s during uninstallation: %w", serviceName, err)
	}

	podName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerPodName)
	if podName == "" {
		log.Error(
			errors.New("runner pod name is missing in metadata"),
			"failed to retrieve runner pod name required for custom module uninstallation",
		)
	}

	pod := &corev1.Pod{}
	err = m.controllerClient.Get(ctx, client.ObjectKey{Name: podName, Namespace: kubernetesCons.OperatorNamespace}, pod)
	if err != nil {
		log.Error(err, "failed to get custom module runner pod for uninstallation", "podName", podName)
	}

	if err = m.controllerClient.Delete(ctx, pod); err != nil {
		return err
	}

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
