package resources

import (
	"fmt"
	"net/url"
)

var _ Resource = HelmModule{}

type HelmValues struct {
	File      *string              `yaml:"file" json:"file"`
	ConfigMap *ConfigMapIndetifier `yaml:"configMap" json:"configMap"`
	Raw       map[string]any       `yaml:"raw" json:"raw"`
}

func (resource HelmValues) Validate() error {
	if (resource.File == nil || *resource.File == "") && resource.ConfigMap == nil && len(resource.Raw) == 0 {
		return fmt.Errorf("HelmValues must specify one of 'file', 'configMap', or 'raw'")
	}

	if resource.ConfigMap != nil {
		if err := resource.ConfigMap.Validate(); err != nil {
			return err
		}
	}

	return nil
}

type HelmModuleSpecChartRepoAuth struct {
	SecretRef *ResourceIndetifier `yaml:"secretRef,omitempty" json:"secretRef,omitempty"`
}

func (resource HelmModuleSpecChartRepoAuth) Validate() error {
	if resource.SecretRef == nil {
		return fmt.Errorf("HelmModuleSpecChartRepoAuth must specify SecretRef")
	}

	if err := resource.SecretRef.Validate(); err != nil {
		return fmt.Errorf("HelmModuleSpecChartRepoAuth SecretRef validation failed: %w", err)
	}

	return nil
}

type HelmModuleSpecChartRepo struct {
	URL       string                       `yaml:"url" json:"url"`
	ChartName string                       `yaml:"chartName" json:"chartName"`
	Version   *string                      `yaml:"version,omitempty" json:"version,omitempty"`
	Auth      *HelmModuleSpecChartRepoAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

func (resource HelmModuleSpecChartRepo) Validate() error {
	if resource.URL == "" {
		return fmt.Errorf("HelmModuleSpecChartRepo URL cannot be empty")
	}

	parsedURL, err := url.Parse(resource.URL)
	if err != nil {
		return fmt.Errorf("HelmModuleSpecChartRepo URL is invalid: %w", err)
	}

	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("HelmModuleSpecChartRepo URL must be a valid URL with scheme and host")
	}

	if resource.ChartName == "" {
		return fmt.Errorf("HelmModuleSpecChartRepo ChartName cannot be empty")
	}

	if resource.Auth != nil {
		if err := resource.Auth.Validate(); err != nil {
			return fmt.Errorf("HelmModuleSpecChartRepo Auth validation failed: %w", err)
		}
	}

	return nil
}

type HelmModuleSpecChartGitAuth struct {
	HTTPSSecretRef *ResourceIndetifier `yaml:"httpsSecretRef,omitempty" json:"httpsSecretRef,omitempty"`
	// SSHSecretRef   SecretIndetifier `yaml:"sshSecretRef"` // TODO
}

func (resource HelmModuleSpecChartGitAuth) Validate() error {
	if resource.HTTPSSecretRef == nil {
		return fmt.Errorf("HelmModuleSpecChartGitAuth must specify at least one authentication method (HTTPSSecretRef)")
	}

	if err := resource.HTTPSSecretRef.Validate(); err != nil {
		return fmt.Errorf("HelmModuleSpecChartGitAuth HTTPSSecretRef validation failed: %w", err)
	}

	return nil
}

type HelmModuleSpecChartGit struct {
	Repo     string                      `yaml:"repo" json:"repo"`
	Path     string                      `yaml:"path" json:"path"`
	Revision *string                     `yaml:"revision,omitempty" json:"revision,omitempty"`
	Auth     *HelmModuleSpecChartGitAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

func (resource HelmModuleSpecChartGit) Validate() error {
	if resource.Repo == "" {
		return fmt.Errorf("HelmModuleSpecChartGit Repo cannot be empty")
	}

	parsedURL, err := url.Parse(resource.Repo)
	if err != nil {
		return fmt.Errorf("HelmModuleSpecChartGit Repo is invalid: %w", err)
	}

	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("HelmModuleSpecChartGit Repo must be a valid URL with scheme and host")
	}

	if resource.Path == "" {
		return fmt.Errorf("HelmModuleSpecChartGit Path cannot be empty")
	}

	if resource.Auth != nil {
		if err := resource.Auth.Validate(); err != nil {
			return fmt.Errorf("HelmModuleSpecChartGit Auth validation failed: %w", err)
		}
	}

	return nil
}

type HelmModuleSpecChart struct {
	Repo      *HelmModuleSpecChartRepo `yaml:"repo" json:"repo"`
	ConfigMap *ConfigMapIndetifier     `yaml:"configMap" json:"configMap"`
	Git       *HelmModuleSpecChartGit  `yaml:"git" json:"git"`
	// OCI // TODO
}

func (resource HelmModuleSpecChart) Validate() error {
	sourceCount := 0
	if resource.Repo != nil {
		sourceCount++
	}
	if resource.ConfigMap != nil {
		sourceCount++
	}
	if resource.Git != nil {
		sourceCount++
	}

	if sourceCount == 0 {
		return fmt.Errorf("HelmModuleSpecChart must specify exactly one chart source: 'repo', 'configMap', or 'git'")
	}

	if sourceCount > 1 {
		return fmt.Errorf("HelmModuleSpecChart must specify only one chart source, but %d were provided", sourceCount)
	}

	if resource.Repo != nil {
		if err := resource.Repo.Validate(); err != nil {
			return fmt.Errorf("HelmModuleSpecChart Repo validation failed: %w", err)
		}
	}

	if resource.ConfigMap != nil {
		if err := resource.ConfigMap.Validate(); err != nil {
			return fmt.Errorf("HelmModuleSpecChart ConfigMap validation failed: %w", err)
		}
	}

	if resource.Git != nil {
		if err := resource.Git.Validate(); err != nil {
			return fmt.Errorf("HelmModuleSpecChart Git validation failed: %w", err)
		}
	}

	return nil
}

type HelmModuleSpec struct {
	Namespace string              `yaml:"namespace" json:"namespace"`
	Chart     HelmModuleSpecChart `yaml:"chart" json:"chart"`
	Values    []HelmValues        `yaml:"values" json:"values"`
	Outputs   []struct {
		Name      string `yaml:"name" json:"name"`
		Value     *any   `yaml:"value,omitempty" json:"value,omitempty"`
		ValueFrom *struct {
			Secret *struct {
				Name      string `yaml:"name" json:"name"`
				Key       string `yaml:"key" json:"key"`
				Namespace string `yaml:"namespace" json:"namespace"`
			} `yaml:"secret,omitempty" json:"secret,omitempty"`
		} `yaml:"valueFrom,omitempty" json:"valueFrom,omitempty"`
	} `yaml:"outputs" json:"outputs"`
	Cleanup *struct {
		RemoveNamespace bool `yaml:"removeNamespace" json:"removeNamespace"`
		RemovePVCs      bool `yaml:"removePVCs" json:"removePVCs"`
	} `yaml:"cleanup,omitempty" json:"cleanup,omitempty"`
	Migration *struct {
		PVC *struct {
			Enabled bool     `yaml:"enabled" json:"enabled"`
			Names   []string `yaml:"names" json:"names"`
		} `yaml:"pvc,omitempty" json:"pvc,omitempty"`
		Secret *struct {
			Enabled bool     `yaml:"enabled" json:"enabled"`
			Names   []string `yaml:"names" json:"names"`
		} `yaml:"secret,omitempty" json:"secret,omitempty"`
		ConfigMap *struct {
			Enabled bool     `yaml:"enabled" json:"enabled"`
			Names   []string `yaml:"names" json:"names"`
		} `yaml:"configMap,omitempty" json:"configMap,omitempty"`
	} `yaml:"migration,omitempty" json:"migration,omitempty"`
}

func (resource HelmModuleSpec) Validate() error {
	if err := resource.Chart.Validate(); err != nil {
		return fmt.Errorf("HelmModuleSpec Chart validation failed: %w", err)
	}

	for i, value := range resource.Values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("HelmModuleSpec values entry %d failed validation: %w", i, err)
		}
	}

	return nil
}

type HelmModule struct {
	BaseResource `yaml:",inline" json:",inline"`
	Spec         HelmModuleSpec `yaml:"spec" json:"spec"`
}

func (module HelmModule) Validate() error {
	return module.Spec.Validate()
}

func (module *HelmModule) RenderSpec(data any) error {
	renderedSpec, err := renderSpecWithTypePreservation(module.Spec, data)
	if err != nil {
		return err
	}

	module.Spec = renderedSpec
	return nil
}

func (module *HelmModule) NewRenderData(config map[string]any, releaseName string) map[string]any {
	return map[string]any{
		"config":      config,
		"releaseName": releaseName,
	}
}
