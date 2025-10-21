package controller

import (
	"context"
	"fmt"
	"os"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
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

		kubeconfigData, ok := secret.Data[workspaceConn.SecretReference.Key]
		if !ok {
			return nil, fmt.Errorf(
				"secret %s/%s does not contain key %s",
				workspaceConn.SecretReference.Namespace,
				workspaceConn.SecretReference.Name,
				workspaceConn.SecretReference.Key,
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

func getModuleSourceType(module *batchv1.Module) string {
	if module.Spec.Helm != nil {
		if module.Spec.Helm.ExistingRelease != nil {
			return "Helm/Adopted"
		}

		return "Helm/Managed"
	}

	if module.Spec.Custom != nil {
		return "Custom"
	}

	return "Unknown"
}
