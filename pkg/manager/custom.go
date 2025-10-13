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
	if err := customModule.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate custom module: %w", err)
	}

	manager := &ModuleCustomManager{controllerClient}

	podName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerPodName)
	podLabel := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerPodUniqueLabel)

	if podName == "" {
		var err error
		podName, podLabel, err = manager.createRunnerPod(ctx, kubernetesConfig, customModule, config)
		if err != nil {
			return nil, err
		}
		metaData[managerCons.CustomMetaDataKeys.RunnerPodName] = podName
		metaData[managerCons.CustomMetaDataKeys.RunnerPodUniqueLabel] = podLabel
	}

	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		var err error
		serviceName, err = manager.createRunnerService(ctx, podLabel)
		if err != nil {
			return nil, err
		}
		metaData[managerCons.CustomMetaDataKeys.RunnerServiceName] = serviceName
	}

	return manager, nil
}

func (m ModuleCustomManager) Install(ctx context.Context, metaData base.MetaData) error {
	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		return errors.New("runner service name is missing in metadata")
	}

	response, err := m.callRunnerEndpoint(ctx, serviceName, "install", metaData)
	if err != nil {
		return err
	}

	if len(response) > 0 {
		metaData[managerCons.CustomMetaDataKeys.InnerMetaData] = response
	}

	return nil
}

func (m ModuleCustomManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	log := logf.FromContext(ctx)

	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		return errors.New("runner service name is missing in metadata")
	}

	if _, err := m.callRunnerEndpoint(ctx, serviceName, "uninstall", metaData); err != nil {
		return err
	}

	service := &corev1.Service{}
	if err := m.controllerClient.Get(ctx, client.ObjectKey{
		Name:      serviceName,
		Namespace: kubernetesCons.OperatorNamespace,
	}, service); err != nil {
		log.Error(err, "failed to get custom module service for uninstallation", "serviceName", serviceName)
	}

	if err := m.controllerClient.Delete(ctx, service); err != nil {
		return fmt.Errorf("failed to delete custom module service %s during uninstallation: %w", serviceName, err)
	}

	podName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerPodName)
	if podName == "" {
		return errors.New("runner pod name is missing in metadata")
	}

	pod := &corev1.Pod{}
	if err := m.controllerClient.Get(ctx, client.ObjectKey{
		Name:      podName,
		Namespace: kubernetesCons.OperatorNamespace,
	}, pod); err != nil {
		log.Error(err, "failed to get custom module runner pod for uninstallation", "podName", podName)
	}

	if err := m.controllerClient.Delete(ctx, pod); err != nil {
		return err
	}

	return nil
}

func (m ModuleCustomManager) Sleep(ctx context.Context, metaData base.MetaData) error {
	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		return errors.New("runner service name is missing in metadata")
	}

	response, err := m.callRunnerEndpoint(ctx, serviceName, "sleep", metaData)
	if err != nil {
		return err
	}

	if len(response) > 0 {
		metaData[managerCons.CustomMetaDataKeys.InnerMetaData] = response
	}

	return nil
}

func (m ModuleCustomManager) Resume(ctx context.Context, metaData base.MetaData) error {
	serviceName := metaData.DecodeToString(managerCons.CustomMetaDataKeys.RunnerServiceName)
	if serviceName == "" {
		return errors.New("runner service name is missing in metadata")
	}

	response, err := m.callRunnerEndpoint(ctx, serviceName, "resume", metaData)
	if err != nil {
		return err
	}

	if len(response) > 0 {
		metaData[managerCons.CustomMetaDataKeys.InnerMetaData] = response
	}

	return nil
}

func (m *ModuleCustomManager) createRunnerPod(
	ctx context.Context,
	kubernetesConfig *rest.Config,
	customModule *resources.CustomModule,
	config map[string]any,
) (podName, podLabel string, err error) {
	log := logf.FromContext(ctx)

	secret, err := m.createRunnerSecret(ctx, kubernetesConfig, customModule, config)
	if err != nil {
		return "", "", err
	}

	defer func() {
		if err := m.controllerClient.Delete(ctx, secret); err != nil {
			log.Error(err, "failed to delete runner secret")
		}
	}()

	podLabel = "custom-manager-runner-" + time.Now().Format("20060102150405")
	pod := m.buildRunnerPod(customModule, secret.Name, podLabel)

	if err := m.controllerClient.Create(ctx, pod); err != nil {
		return "", "", err
	}

	if err := m.waitForPodRunning(ctx, pod); err != nil {
		if deleteErr := m.controllerClient.Delete(ctx, pod); deleteErr != nil {
			log.Error(deleteErr, "failed to delete pod after error")
		}
		return "", "", err
	}

	return pod.Name, podLabel, nil
}

func (m *ModuleCustomManager) createRunnerSecret(
	ctx context.Context,
	kubernetesConfig *rest.Config,
	customModule *resources.CustomModule,
	config map[string]any,
) (*corev1.Secret, error) {
	log := logf.FromContext(ctx)

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
		default:
			err := fmt.Errorf("unsupported custom module permission: %s", permission)
			log.Error(err, "encountered an unsupported permission during custom module secret generation")
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
		Type:      corev1.SecretTypeOpaque,
	}

	if err := m.controllerClient.Create(ctx, secret); err != nil {
		return nil, fmt.Errorf("failed to create custom module secret: %w", err)
	}

	return secret, nil
}

func (m *ModuleCustomManager) buildRunnerPod(
	customModule *resources.CustomModule,
	secretName, podLabel string,
) *corev1.Pod {
	imagePullSecrets := make([]corev1.LocalObjectReference, len(customModule.Spec.ImagePullSecrets))
	for i, imagePullSecret := range customModule.Spec.ImagePullSecrets {
		imagePullSecrets[i] = corev1.LocalObjectReference{Name: imagePullSecret}
	}

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
							corev1.ResourceEphemeralStorage: resource.MustParse("500Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceEphemeralStorage: resource.MustParse("5Gi"),
						},
					},
					Name:  "worker",
					Image: customModule.Spec.Image,
					Ports: []corev1.ContainerPort{
						{ContainerPort: 8080},
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

	return pod
}

func (m *ModuleCustomManager) waitForPodRunning(ctx context.Context, pod *corev1.Pod) error {
	for {
		if err := m.controllerClient.Get(ctx, client.ObjectKeyFromObject(pod), pod); err != nil {
			return err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case "", corev1.PodPending:
			time.Sleep(time.Second * 2)
		case corev1.PodSucceeded:
			return fmt.Errorf("custom module runner pod succeeded unexpectedly: pod phase %s", pod.Status.Phase)
		case corev1.PodFailed:
			return fmt.Errorf("custom module runner pod failed: pod phase %s", pod.Status.Phase)
		default:
			return fmt.Errorf("custom module runner pod entered an unexpected phase: %s", pod.Status.Phase)
		}
	}
}

func (m *ModuleCustomManager) createRunnerService(ctx context.Context, podLabel string) (string, error) {
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

	if err := m.controllerClient.Create(ctx, service); err != nil {
		return "", err
	}

	return service.Name, nil
}

func (m *ModuleCustomManager) callRunnerEndpoint(
	ctx context.Context,
	serviceName, endpoint string,
	metaData base.MetaData,
) (map[string]any, error) {
	log := logf.FromContext(ctx)

	jsonBytes := m.encodeMetaData(ctx, metaData)

	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:80/%s", serviceName, kubernetesCons.OperatorNamespace, endpoint)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return nil, fmt.Errorf(
			"failed to %s custom module %s: received unexpected status code %d, expected 204",
			endpoint, serviceName, resp.StatusCode,
		)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body for custom module %s: %w", endpoint, err)
	}

	if len(responseBody) == 0 {
		return nil, nil
	}

	var response map[string]any
	if err := json.Unmarshal(responseBody, &response); err != nil {
		log.Error(err, "failed to unmarshal response body", "endpoint", endpoint, "response", string(responseBody))
		return nil, fmt.Errorf("failed to unmarshal %s response body after successful request: %w", endpoint, err)
	}

	return response, nil
}

func (m *ModuleCustomManager) encodeMetaData(ctx context.Context, metaData base.MetaData) []byte {
	log := logf.FromContext(ctx)

	decodedMap := make(map[string]any)
	if err := mapstructure.Decode(metaData[managerCons.CustomMetaDataKeys.InnerMetaData], &decodedMap); err != nil {
		log.Error(err, "failed to decode InnerMetaData")
	}

	jsonBytes, err := json.Marshal(decodedMap)
	if err != nil {
		log.Error(err, "failed to marshal decoded InnerMetaData to JSON")
		return []byte("{}")
	}

	return jsonBytes
}
