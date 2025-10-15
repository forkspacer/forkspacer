package resources

import (
	"fmt"
	"net/url"
)

var _ Resource = HelmModule{}

type HelmValues struct {
	File      *string             `yaml:"file"`
	ConfigMap *ResourceIndetifier `yaml:"configMap"`
	Raw       map[string]any      `yaml:"raw"`
}

func (resource HelmValues) Valdiate() error {
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

type HelmModuleSpec struct {
	Namespace string       `yaml:"namespace"`
	Repo      string       `yaml:"repo"`
	ChartName string       `yaml:"chartName"`
	Version   string       `yaml:"version"`
	Values    []HelmValues `yaml:"values"`
	Outputs   []struct {
		Name      string `yaml:"name"`
		Value     *any   `yaml:"value,omitempty"`
		ValueFrom *struct {
			Secret *struct {
				Name      string `yaml:"name"`
				Key       string `yaml:"key"`
				Namespace string `yaml:"namespace"`
			} `yaml:"secret,omitempty"`
		} `yaml:"valueFrom,omitempty"`
	} `yaml:"outputs"`
	Cleanup *struct {
		RemoveNamespace bool `yaml:"removeNamespace"`
		RemovePVCs      bool `yaml:"removePVCs"`
	} `yaml:"cleanup,omitempty"`
	Migration *struct {
		PVC *struct {
			Enabled bool     `yaml:"enabled"`
			Names   []string `yaml:"names"`
		} `yaml:"pvc,omitempty"`
		Secret *struct {
			Enabled bool     `yaml:"enabled"`
			Names   []string `yaml:"names"`
		} `yaml:"secret,omitempty"`
		ConfigMap *struct {
			Enabled bool     `yaml:"enabled"`
			Names   []string `yaml:"names"`
		} `yaml:"configMap,omitempty"`
	} `yaml:"migration,omitempty"`
}

func (resource HelmModuleSpec) Validate() error {
	for i, value := range resource.Values {
		if err := value.Valdiate(); err != nil {
			return fmt.Errorf("HelmModuleSpec values entry %d failed validation: %w", i, err)
		}
	}

	_, err := url.Parse(resource.Repo)
	if err != nil {
		return fmt.Errorf("HelmModuleSpec Repo '%s' is invalid: %w", resource.Repo, err)
	}

	if resource.Version == "" {
		return fmt.Errorf("HelmModuleSpec Version cannot be empty")
	}

	return nil
}

type HelmModule struct {
	BaseResource `yaml:",inline"`
	Spec         HelmModuleSpec `yaml:"spec"`
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
