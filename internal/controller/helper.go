package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

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
	"k8s.io/client-go/util/homedir"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func NewKubernetesConfig(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
	controllerClient client.Client,
) (*rest.Config, error) {
	var (
		config *rest.Config
		err    error
	)

	switch workspaceConn.Type {
	case batchv1.WorkspaceConnectionTypeInCluster:
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}

	case batchv1.WorkspaceConnectionTypeLocal:
		config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(homedir.HomeDir(), ".kube", "config"))
		if err != nil {
			return nil, err
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

		kubeconfigData, exists := secret.Data[secretKey]
		if !exists {
			return nil, fmt.Errorf("secret %s/%s does not contain key %q",
				workspaceConn.SecretReference.Namespace, workspaceConn.SecretReference.Name, secretKey)
		}

		clientConfig, _ := clientcmd.NewClientConfigFromBytes(kubeconfigData)
		config, err = clientConfig.ClientConfig()
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown workspace connection type: %s", workspaceConn.Type)
	}

	return config, nil
}

func NewHelmService(
	ctx context.Context,
	workspaceConn *batchv1.WorkspaceConnection,
	controllerClient client.Client,
) (*services.HelmService, error) {
	kubernetesConfig, err := NewKubernetesConfig(ctx, workspaceConn, controllerClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	log := logf.FromContext(ctx, "HELM", batchv1.WorkspaceConnectionTypeLocal+"/"+batchv1.WorkspaceConnectionTypeInCluster)
	helmService, err := services.NewHelmService(
		kubernetesConfig,
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

// ConvertRestConfigToAPIConfig converts a *rest.Config to an api.Config (kubeconfig format)
func ConvertRestConfigToAPIConfig(
	restConfig *rest.Config,
	contextName, clusterName, userName string,
) *clientcmdapi.Config {
	if contextName == "" {
		contextName = "default"
	}
	if clusterName == "" {
		clusterName = "default"
	}
	if userName == "" {
		userName = "default"
	}

	// Create the cluster configuration
	cluster := &clientcmdapi.Cluster{
		Server:                   restConfig.Host,
		CertificateAuthorityData: restConfig.CAData,
		CertificateAuthority:     restConfig.CAFile,
		InsecureSkipTLSVerify:    restConfig.Insecure,
		TLSServerName:            restConfig.ServerName,
	}

	// Create the auth info (user credentials)
	authInfo := &clientcmdapi.AuthInfo{
		ClientCertificateData: restConfig.CertData,
		ClientCertificate:     restConfig.CertFile,
		ClientKeyData:         restConfig.KeyData,
		ClientKey:             restConfig.KeyFile,
		Token:                 restConfig.BearerToken,
		TokenFile:             restConfig.BearerTokenFile,
		Impersonate:           restConfig.Impersonate.UserName,
		ImpersonateGroups:     restConfig.Impersonate.Groups,
		ImpersonateUserExtra:  restConfig.Impersonate.Extra,
		Username:              restConfig.Username,
		Password:              restConfig.Password,
	}

	// Handle exec plugin if present
	if restConfig.ExecProvider != nil {
		authInfo.Exec = &clientcmdapi.ExecConfig{
			Command:            restConfig.ExecProvider.Command,
			Args:               restConfig.ExecProvider.Args,
			APIVersion:         restConfig.ExecProvider.APIVersion,
			InstallHint:        restConfig.ExecProvider.InstallHint,
			ProvideClusterInfo: restConfig.ExecProvider.ProvideClusterInfo,
		}

		// Convert environment variables
		if restConfig.ExecProvider.Env != nil {
			authInfo.Exec.Env = make([]clientcmdapi.ExecEnvVar, len(restConfig.ExecProvider.Env))
			for i, env := range restConfig.ExecProvider.Env {
				authInfo.Exec.Env[i] = clientcmdapi.ExecEnvVar{
					Name:  env.Name,
					Value: env.Value,
				}
			}
		}
	}

	// Create the context
	kubeContext := &clientcmdapi.Context{
		Cluster:   clusterName,
		AuthInfo:  userName,
		Namespace: "default",
	}

	// Build the final Config
	config := &clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: cluster,
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			userName: authInfo,
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: kubeContext,
		},
		CurrentContext: contextName,
	}

	return config
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
