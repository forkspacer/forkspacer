package utils

import (
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

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
