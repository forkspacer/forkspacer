package services

import (
	"context"
	"fmt"
	"os"
	"time"

	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type helmConfigFlags struct {
	config    *rest.Config
	namespace string
}

func (c *helmConfigFlags) ToRESTConfig() (*rest.Config, error) {
	return c.config, nil
}

func (c *helmConfigFlags) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(c.config)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(discoveryClient), nil
}

func (c *helmConfigFlags) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := c.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	return mapper, nil
}

func (c *helmConfigFlags) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return &helmClientConfig{
		namespace: c.namespace,
		config:    c.config,
	}
}

func (c *helmConfigFlags) ClientConfig() clientcmd.ClientConfig {
	return &helmClientConfig{namespace: c.namespace, config: c.config}
}

func (c *helmConfigFlags) Namespace() (string, bool, error) {
	return c.namespace, false, nil
}

type helmClientConfig struct {
	namespace string
	config    *rest.Config
}

func (c *helmClientConfig) Namespace() (string, bool, error) {
	return c.namespace, false, nil
}

func (c *helmClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return nil
}

func (c *helmClientConfig) RawConfig() (clientcmdapi.Config, error) {
	return clientcmdapi.Config{}, nil
}

func (c *helmClientConfig) ClientConfig() (*rest.Config, error) {
	return c.config, nil
}

type HelmService struct {
	settings         *cli.EnvSettings
	actionConfig     *action.Configuration
	kubernetesConfig *rest.Config
	kubernetesClient *kubernetes.Clientset
}

func NewHelmService(
	kubernetesConfig *rest.Config,
	debugLogger action.DebugLog,
) (*HelmService, error) {
	settings := cli.New()
	actionConfig := new(action.Configuration)

	if err := actionConfig.Init(
		&helmConfigFlags{
			config:    kubernetesConfig,
			namespace: "default",
		},
		"default",
		os.Getenv("HELM_DRIVER"),
		debugLogger,
	); err != nil {
		return nil, err
	}

	kubernetesClient, err := kubernetes.NewForConfig(kubernetesConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %v", err)
	}

	return &HelmService{
		settings:         settings,
		actionConfig:     actionConfig,
		kubernetesConfig: kubernetesConfig,
		kubernetesClient: kubernetesClient,
	}, nil
}

func (service HelmService) InstallFromRepository(
	ctx context.Context,
	chartName, releaseName, namespace, repoURL, version string,
	wait bool,
	helmValues []resources.HelmValues,
) error {
	actionClient := action.NewInstall(service.actionConfig)
	actionClient.Namespace = namespace
	actionClient.CreateNamespace = true
	actionClient.RepoURL = repoURL
	actionClient.ReleaseName = releaseName
	actionClient.Version = version
	actionClient.Wait = wait
	actionClient.Timeout = 15 * time.Minute

	mergedValues, err := service.MergeHelmValues(ctx, helmValues)
	if err != nil {
		return err
	}

	chartPath, err := actionClient.LocateChart(chartName, service.settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart %s: %w", chartName, err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart from repository: %w", err)
	}

	_, err = actionClient.RunWithContext(ctx, chart, mergedValues)
	if err != nil {
		go func() {
			_ = service.UninstallRelease(ctx, releaseName, namespace, true, true)
		}()
		return fmt.Errorf("failed to install chart from repository: %w", err)
	}

	return nil
}

func (service HelmService) UninstallRelease(
	ctx context.Context,
	releaseName, namespace string,
	removeNamespace, removePVCs bool,
) error {
	actionClient := action.NewUninstall(service.actionConfig)
	actionClient.Wait = true
	actionClient.Timeout = 15 * time.Minute
	actionClient.IgnoreNotFound = true
	actionClient.DeletionPropagation = "foreground"

	_, err := actionClient.Run(releaseName)
	if err != nil {
		return fmt.Errorf("failed to uninstall release: %w", err)
	}

	if removeNamespace && namespace != "default" && namespace != "" { //nolint:goconst
		err = service.kubernetesClient.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete namespace '%s': %w", namespace, err)
		}
	} else if removePVCs {
		err = service.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).DeleteCollection(
			ctx,
			metav1.DeleteOptions{},
			metav1.ListOptions{
				LabelSelector: "app.kubernetes.io/instance=" + releaseName,
			},
		)
		if err != nil {
			return fmt.Errorf(
				"failed to delete PersistentVolumeClaims for release '%s' in namespace '%s': %w",
				releaseName, namespace, err,
			)
		}
	}

	return nil
}

type ResourceReplicaCount struct {
	Namespace string
	Name      string
	Replicas  int32
}

func (service HelmService) ScaleDeployments( //nolint:dupl
	ctx context.Context,
	releaseName, namespace string,
	replicas int32,
) ([]ResourceReplicaCount, error) {
	deploymentList, err := service.kubernetesClient.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + releaseName,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"failed to list deployments for release '%s' in namespace '%s': %w",
			releaseName, namespace, err,
		)
	}

	oldReplicas := make([]ResourceReplicaCount, 0, len(deploymentList.Items))

	for _, deployment := range deploymentList.Items {
		var currentReplicas int32 = 0
		if deployment.Spec.Replicas != nil {
			currentReplicas = *deployment.Spec.Replicas
		}

		oldReplicas = append(oldReplicas, ResourceReplicaCount{
			Namespace: deployment.Namespace,
			Name:      deployment.Name,
			Replicas:  currentReplicas,
		})

		if currentReplicas == replicas {
			continue
		}

		_, err = service.kubernetesClient.AppsV1().Deployments(namespace).Patch(
			ctx,
			deployment.Name,
			types.StrategicMergePatchType,
			fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, replicas),
			metav1.PatchOptions{},
		)
		if err != nil {
			return oldReplicas, fmt.Errorf(
				"failed to patch deployment '%s' in namespace '%s': %w",
				deployment.Name, deployment.Namespace, err,
			)
		}
	}

	return oldReplicas, nil
}

func (service HelmService) ScaleDeploymentsBack(ctx context.Context, deploymentReplicas ...ResourceReplicaCount) error {
	for _, deploymentReplica := range deploymentReplicas {
		_, err := service.kubernetesClient.AppsV1().Deployments(deploymentReplica.Namespace).Patch(
			ctx,
			deploymentReplica.Name,
			types.StrategicMergePatchType,
			fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, deploymentReplica.Replicas),
			metav1.PatchOptions{},
		)
		if err != nil {
			return fmt.Errorf(
				"failed to patch deployment '%s' in namespace '%s': %w",
				deploymentReplica.Name, deploymentReplica.Namespace, err,
			)
		}
	}

	return nil
}

func (service HelmService) ScaleReplicaSets( //nolint:dupl
	ctx context.Context,
	releaseName, namespace string,
	replicas int32,
) ([]ResourceReplicaCount, error) {
	replicaSetList, err := service.kubernetesClient.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + releaseName,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"failed to list replica sets for release '%s' in namespace '%s': %w",
			releaseName, namespace, err,
		)
	}

	oldReplicas := make([]ResourceReplicaCount, 0, len(replicaSetList.Items))

	for _, replicaSet := range replicaSetList.Items {
		var currentReplicas int32 = 0
		if replicaSet.Spec.Replicas != nil {
			currentReplicas = *replicaSet.Spec.Replicas
		}

		oldReplicas = append(oldReplicas, ResourceReplicaCount{
			Namespace: replicaSet.Namespace,
			Name:      replicaSet.Name,
			Replicas:  currentReplicas,
		})

		if currentReplicas == replicas {
			continue
		}

		_, err = service.kubernetesClient.AppsV1().ReplicaSets(namespace).Patch(
			ctx,
			replicaSet.Name,
			types.StrategicMergePatchType,
			fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, replicas),
			metav1.PatchOptions{},
		)
		if err != nil {
			return oldReplicas, fmt.Errorf(
				"failed to patch replica set '%s' in namespace '%s': %w",
				replicaSet.Name, replicaSet.Namespace, err,
			)
		}
	}

	return oldReplicas, nil
}

func (service HelmService) ScaleReplicaSetsBack(ctx context.Context, replicaSetReplicas ...ResourceReplicaCount) error {
	for _, replicaSetReplica := range replicaSetReplicas {
		_, err := service.kubernetesClient.AppsV1().ReplicaSets(replicaSetReplica.Namespace).Patch(
			ctx,
			replicaSetReplica.Name,
			types.StrategicMergePatchType,
			fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, replicaSetReplica.Replicas),
			metav1.PatchOptions{},
		)
		if err != nil {
			return fmt.Errorf(
				"failed to patch replicaSet '%s' in namespace '%s': %w",
				replicaSetReplica.Name, replicaSetReplica.Namespace, err,
			)
		}
	}

	return nil
}

func (service HelmService) ScaleStatefulSets( //nolint:dupl
	ctx context.Context,
	releaseName, namespace string,
	replicas int32,
) ([]ResourceReplicaCount, error) {
	statefulSetList, err := service.kubernetesClient.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + releaseName,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"failed to list stateful sets for release '%s' in namespace '%s': %w",
			releaseName, namespace, err,
		)
	}

	oldReplicas := make([]ResourceReplicaCount, 0, len(statefulSetList.Items))

	for _, statefulSet := range statefulSetList.Items {
		var currentReplicas int32 = 0
		if statefulSet.Spec.Replicas != nil {
			currentReplicas = *statefulSet.Spec.Replicas
		}

		oldReplicas = append(oldReplicas, ResourceReplicaCount{
			Namespace: statefulSet.Namespace,
			Name:      statefulSet.Name,
			Replicas:  currentReplicas,
		})

		if currentReplicas == replicas {
			continue
		}

		_, err = service.kubernetesClient.AppsV1().StatefulSets(namespace).Patch(
			ctx,
			statefulSet.Name,
			types.StrategicMergePatchType,
			fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, replicas),
			metav1.PatchOptions{},
		)
		if err != nil {
			return oldReplicas, fmt.Errorf(
				"failed to patch stateful set '%s' in namespace '%s': %w",
				statefulSet.Name, statefulSet.Namespace, err,
			)
		}
	}

	return oldReplicas, nil
}

func (service HelmService) ScaleStatefulSetsBack(
	ctx context.Context,
	satefulSetReplicas ...ResourceReplicaCount,
) error {
	for _, satefulSetReplica := range satefulSetReplicas {
		_, err := service.kubernetesClient.AppsV1().StatefulSets(satefulSetReplica.Namespace).Patch(
			ctx,
			satefulSetReplica.Name,
			types.StrategicMergePatchType,
			fmt.Appendf(nil, `{"spec":{"replicas":%d}}`, satefulSetReplica.Replicas),
			metav1.PatchOptions{},
		)
		if err != nil {
			return fmt.Errorf(
				"failed to patch satefulSet '%s' in namespace '%s': %w",
				satefulSetReplica.Name, satefulSetReplica.Namespace, err,
			)
		}
	}

	return nil
}

func (service HelmService) MergeHelmValues(
	ctx context.Context,
	helmValues []resources.HelmValues,
) (map[string]any, error) {
	valuesSlice := make([]map[string]any, 0)

	for _, helmValue := range helmValues {
		if helmValue.File != nil && *helmValue.File != "" {
			valueOpts := &values.Options{
				ValueFiles: []string{*helmValue.File},
			}
			fileValues, err := valueOpts.MergeValues(getter.All(service.settings))
			if err != nil {
				return nil, fmt.Errorf("failed to read values file '%s': %w", *helmValue.File, err)
			}
			valuesSlice = append(valuesSlice, fileValues)
		}

		if helmValue.ConfigMap != nil && helmValue.ConfigMap.Name != "" {
			configMapNamespace := helmValue.ConfigMap.Namespace
			if configMapNamespace == "" {
				configMapNamespace = "default"
			}

			configMap, err := service.kubernetesClient.
				CoreV1().
				ConfigMaps(configMapNamespace).
				Get(ctx, helmValue.ConfigMap.Name, metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf(
					"failed to get ConfigMap '%s' in namespace '%s': %w",
					helmValue.ConfigMap.Name, configMapNamespace, err,
				)
			}

			if configMapValues := configMap.Data[kubernetesCons.Helm.ValuesConfigMapKey]; configMapValues != "" {
				parsedConfigMapValues, err := chartutil.ReadValues([]byte(configMapValues))
				if err != nil {
					return nil, fmt.Errorf(
						"failed to read values from ConfigMap '%s' in namespace '%s': %w",
						helmValue.ConfigMap.Name, configMapNamespace, err,
					)
				}
				valuesSlice = append(valuesSlice, parsedConfigMapValues)
			}
		}

		if len(helmValue.Raw) > 0 {
			valuesSlice = append(valuesSlice, helmValue.Raw)
		}
	}

	mergedValues := make(map[string]any)
	if len(valuesSlice) > 0 {
		mergedValues = valuesSlice[0]
		for _, v := range valuesSlice[1:] {
			mergedValues = chartutil.CoalesceTables(v, mergedValues)
		}
	}

	return mergedValues, nil
}

func (service HelmService) GetSecretValue(ctx context.Context, namespace, name, key string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}

	secret, err := service.kubernetesClient.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	secretValue, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("")
	}

	return string(secretValue), nil
}

func (service HelmService) ListPVCs(
	ctx context.Context,
	releaseName, namespace string,
) (*corev1.PersistentVolumeClaimList, error) {
	pvcList, err := service.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + releaseName,
	})
	if err != nil {
		return nil, err
	}

	return pvcList, nil
}

// GetRelease retrieves information about an existing Helm release
func (service HelmService) GetRelease(releaseName, namespace string) (*release.Release, error) {
	actionClient := action.NewGet(service.actionConfig)

	rel, err := actionClient.Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("failed to get release '%s' in namespace '%s': %w", releaseName, namespace, err)
	}

	return rel, nil
}

// GetReleaseManifest retrieves the rendered manifest of a Helm release
func (service HelmService) GetReleaseManifest(releaseName, namespace string) (string, error) {
	release, err := service.GetRelease(releaseName, namespace)
	if err != nil {
		return "", err
	}

	return release.Manifest, nil
}

// GetReleaseValues retrieves the values used for a Helm release
func (service HelmService) GetReleaseValues(releaseName, namespace string) (map[string]interface{}, error) {
	release, err := service.GetRelease(releaseName, namespace)
	if err != nil {
		return nil, err
	}

	return release.Config, nil
}
