package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	managerBase "github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func NewAPIConfig(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
	controllerClient client.Client,
) (*clientcmdapi.Config, error) {
	var (
		config *clientcmdapi.Config
		err    error
	)

	switch workspaceConn.Type {
	case batchv1.WorkspaceConnectionTypeInCluster:
		// Initialize the config object and its maps before populating them
		config = &clientcmdapi.Config{
			Clusters:  make(map[string]*clientcmdapi.Cluster),
			AuthInfos: make(map[string]*clientcmdapi.AuthInfo),
			Contexts:  make(map[string]*clientcmdapi.Context),
		}

		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster REST config: %w", err)
		}

		// Handle CA certificate
		cluster := &clientcmdapi.Cluster{
			Server: restConfig.Host,
		}
		if len(restConfig.CAData) > 0 {
			cluster.CertificateAuthorityData = restConfig.CAData
		} else if restConfig.CAFile != "" {
			cluster.CertificateAuthority = restConfig.CAFile
		}
		if restConfig.Insecure {
			cluster.InsecureSkipTLSVerify = true
		}
		config.Clusters["default-cluster"] = cluster

		// Handle authentication
		authInfo := &clientcmdapi.AuthInfo{}
		if restConfig.BearerToken != "" {
			authInfo.Token = restConfig.BearerToken
		} else if restConfig.BearerTokenFile != "" {
			// Read token from file
			token, err := os.ReadFile(restConfig.BearerTokenFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read bearer token file %s: %w", restConfig.BearerTokenFile, err)
			}
			authInfo.Token = string(token)
		}
		// Handle client certificates if present
		if len(restConfig.CertData) > 0 {
			authInfo.ClientCertificateData = restConfig.CertData
		} else if restConfig.CertFile != "" {
			authInfo.ClientCertificate = restConfig.CertFile
		}
		if len(restConfig.KeyData) > 0 {
			authInfo.ClientKeyData = restConfig.KeyData
		} else if restConfig.KeyFile != "" {
			authInfo.ClientKey = restConfig.KeyFile
		}
		config.AuthInfos["default-user"] = authInfo

		config.Contexts["default-context"] = &clientcmdapi.Context{
			Cluster:  "default-cluster",
			AuthInfo: "default-user",
		}
		config.CurrentContext = "default-context"

	case batchv1.WorkspaceConnectionTypeLocal:
		config, err = clientcmd.LoadFromFile(clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load local kubeconfig from %s: %w", clientcmd.RecommendedHomeFile, err)
		}

	case batchv1.WorkspaceConnectionTypeKubeconfig:
		secret := &corev1.Secret{}
		err := controllerClient.Get(ctx, client.ObjectKey{
			Name:      workspaceConn.SecretReference.Name,
			Namespace: workspaceConn.SecretReference.Namespace,
		}, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s/%s: %w",
				workspaceConn.SecretReference.Namespace, workspaceConn.SecretReference.Name, err)
		}

		// Use custom key if provided, otherwise use default "kubeconfig"
		secretKey := workspaceConn.SecretReference.Key
		if secretKey == "" {
			secretKey = kubernetesCons.WorkspaceSecretKeys.KubeConfig
		}

		kubeconfigData, ok := secret.Data[secretKey]
		if !ok {
			return nil, fmt.Errorf(
				"secret %s/%s does not contain key %s",
				workspaceConn.SecretReference.Namespace,
				workspaceConn.SecretReference.Name,
				secretKey,
			)
		}

		config, err = clientcmd.Load(kubeconfigData)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig from secret data: %w", err)
		}

	default:
		return nil, fmt.Errorf("unknown workspace connection type: %s", workspaceConn.Type)
	}

	return config, nil
}

func NewRESTConfig(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
	controllerClient client.Client,
) (*rest.Config, error) {
	apiConfig, err := NewAPIConfig(ctx, workspaceConn, controllerClient)
	if err != nil {
		return nil, err
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*apiConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}

	return restConfig, nil
}

func NewHelmService(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
	controllerClient client.Client,
) (*services.HelmService, error) {
	restConfig, err := NewRESTConfig(ctx, workspaceConn, controllerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes REST client: %w", err)
	}

	log := logf.FromContext(ctx, "HELM", batchv1.WorkspaceConnectionTypeLocal+"/"+batchv1.WorkspaceConnectionTypeInCluster)
	helmService, err := services.NewHelmService(
		restConfig,
		func(format string, v ...any) {
			log.V(1).Info(fmt.Sprintf(format, v))
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Helm service: %w", err)
	}

	return helmService, nil
}

func newHelmReleaseNameFromModule(module batchv1.Module) string {
	return module.Namespace + "-" + module.Name
}

// IsHelmModule checks if a module is a Helm module by examining its resource annotation
func IsHelmModule(module *batchv1.Module) bool {
	resourceAnnotation := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return false
	}

	err := resources.HandleResource([]byte(resourceAnnotation), nil,
		func(helmModule resources.HelmModule) error { return nil },
		func(_ resources.CustomModule) error {
			return errors.New("not supported")
		},
	)
	return err == nil
}

// ParseHelmModule parses a Helm module from its annotations and renders its spec
func ParseHelmModule(ctx context.Context, module *batchv1.Module) (resources.HelmModule, error) {
	log := logf.FromContext(ctx)

	resourceAnnotation := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return resources.HelmModule{},
			fmt.Errorf("module %s/%s is missing resource annotation", module.Namespace, module.Name)
	}

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]
	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		if err := metaData.Parse([]byte(managerData)); err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	var helmModule resources.HelmModule
	err := resources.HandleResource([]byte(resourceAnnotation), &configMap,
		func(hm resources.HelmModule) error {
			releaseName, ok := metaData[managerCons.HelmMetaDataKeys.ReleaseName].(string)
			if !ok {
				return fmt.Errorf("module release name not found in manager metadata or not a string")
			}

			if err := hm.RenderSpec(hm.NewRenderData(configMap, releaseName)); err != nil {
				return fmt.Errorf("failed to render Helm module spec: %w", err)
			}
			helmModule = hm
			return nil
		},
		nil,
	)
	if err != nil {
		return resources.HelmModule{}, err
	}

	return helmModule, nil
}

// getModuleSourceType determines the source type string for a module
// Returns format: "Adopted/Helm", "Managed/Git", "Managed/Repository", etc.
func getModuleSourceType(module *batchv1.Module) string {
	source := &module.Spec.Source

	// Check if this was an adopted release (even if later converted to Raw)
	if module.Spec.Source.ExistingHelmRelease != nil {
		return "Adopted/Helm"
	}

	// Check annotations for previously adopted releases
	if module.Annotations != nil {
		if _, exists := module.Annotations["forkspacer.com/adopted-release"]; exists {
			return "Adopted/Helm"
		}
	}

	// For managed modules, determine the source type
	if source.Raw != nil {
		// Check if this came from an adopted release by looking at resource annotation
		if module.Annotations != nil {
			resource := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
			if resource != "" {
				// If the resource annotation exists, it's from an adopted release that was converted
				// This handles forked modules from adopted releases
				var helmModule resources.HelmModule
				configMap := make(map[string]any)
				err := resources.HandleResource([]byte(resource), &configMap,
					func(hm resources.HelmModule) error {
						helmModule = hm
						return nil
					},
					nil,
				)
				if err == nil {
					// Determine source from the HelmModule spec
					if helmModule.Spec.Chart.Git != nil {
						return "Managed/Git"
					} else if helmModule.Spec.Chart.Repo != nil {
						return "Managed/Repository"
					} else if helmModule.Spec.Chart.ConfigMap != nil {
						return "Managed/ConfigMap"
					}
				}
			}
		}
		return "Managed/Raw"
	}

	if source.ConfigMap != nil {
		return "Managed/ConfigMap"
	}

	if source.HttpURL != nil {
		return "Managed/HTTP"
	}

	if source.Github != nil {
		return "Managed/GitHub"
	}

	return "Unknown"
}
