package resources

import (
	"fmt"
)

var _ Resource = ArgoCDModule{}

type ArgoCDSyncPolicy struct {
	Automated *struct {
		Prune      bool `yaml:"prune"`
		SelfHeal   bool `yaml:"selfHeal"`
		AllowEmpty bool `yaml:"allowEmpty"`
	} `yaml:"automated,omitempty"`
	SyncOptions []string `yaml:"syncOptions,omitempty"`
	Retry       *struct {
		Limit   int `yaml:"limit"`
		Backoff struct {
			Duration    string `yaml:"duration"`
			Factor      *int   `yaml:"factor,omitempty"`
			MaxDuration string `yaml:"maxDuration"`
		} `yaml:"backoff"`
	} `yaml:"retry,omitempty"`
}

type ArgoCDDestination struct {
	Server    *string `yaml:"server,omitempty"`
	Name      *string `yaml:"name,omitempty"`
	Namespace string  `yaml:"namespace"`
}

func (d ArgoCDDestination) Validate() error {
	if d.Server == nil && d.Name == nil {
		return fmt.Errorf("ArgoCDDestination must specify either 'server' or 'name'")
	}
	if d.Server != nil && d.Name != nil {
		return fmt.Errorf("ArgoCDDestination cannot specify both 'server' and 'name'")
	}
	if d.Namespace == "" {
		return fmt.Errorf("ArgoCDDestination namespace cannot be empty")
	}
	return nil
}

type ArgoCDSource struct {
	RepoURL        string  `yaml:"repoURL"`
	Path           *string `yaml:"path,omitempty"`
	Chart          *string `yaml:"chart,omitempty"`
	TargetRevision string  `yaml:"targetRevision"`
	Helm           *struct {
		ReleaseName    string         `yaml:"releaseName,omitempty"`
		Values         string         `yaml:"values,omitempty"`
		ValueFiles     []string       `yaml:"valueFiles,omitempty"`
		Parameters     []HelmKeyValue `yaml:"parameters,omitempty"`
		FileParameters []struct {
			Name string `yaml:"name"`
			Path string `yaml:"path"`
		} `yaml:"fileParameters,omitempty"`
	} `yaml:"helm,omitempty"`
	Kustomize *struct {
		NamePrefix   string            `yaml:"namePrefix,omitempty"`
		NameSuffix   string            `yaml:"nameSuffix,omitempty"`
		Images       []string          `yaml:"images,omitempty"`
		CommonLabels map[string]string `yaml:"commonLabels,omitempty"`
		Version      string            `yaml:"version,omitempty"`
	} `yaml:"kustomize,omitempty"`
}

type HelmKeyValue struct {
	Name        string `yaml:"name"`
	Value       string `yaml:"value"`
	ForceString bool   `yaml:"forceString,omitempty"`
}

func (s ArgoCDSource) Validate() error {
	if s.RepoURL == "" {
		return fmt.Errorf("ArgoCDSource repoURL cannot be empty")
	}
	if s.TargetRevision == "" {
		return fmt.Errorf("ArgoCDSource targetRevision cannot be empty")
	}
	if s.Path == nil && s.Chart == nil {
		return fmt.Errorf("ArgoCDSource must specify either 'path' or 'chart'")
	}
	if s.Path != nil && s.Chart != nil {
		return fmt.Errorf("ArgoCDSource cannot specify both 'path' and 'chart'")
	}
	return nil
}

type ArgoCDModuleSpec struct {
	ApplicationName   string            `yaml:"applicationName"`
	Project           string            `yaml:"project"`
	Source            ArgoCDSource      `yaml:"source"`
	Destination       ArgoCDDestination `yaml:"destination"`
	SyncPolicy        *ArgoCDSyncPolicy `yaml:"syncPolicy,omitempty"`
	IgnoreDifferences []struct {
		Group        string   `yaml:"group"`
		Kind         string   `yaml:"kind"`
		Name         string   `yaml:"name,omitempty"`
		JSONPointers []string `yaml:"jsonPointers,omitempty"`
	} `yaml:"ignoreDifferences,omitempty"`
	Info []struct {
		Name  string `yaml:"name"`
		Value string `yaml:"value"`
	} `yaml:"info,omitempty"`
	Cleanup struct {
		Cascade bool `yaml:"cascade"`
	} `yaml:"cleanup"`
}

func (s ArgoCDModuleSpec) Validate() error {
	if s.ApplicationName == "" {
		return fmt.Errorf("ArgoCDModuleSpec applicationName cannot be empty")
	}
	if s.Project == "" {
		return fmt.Errorf("ArgoCDModuleSpec project cannot be empty")
	}
	if err := s.Source.Validate(); err != nil {
		return fmt.Errorf("ArgoCDModuleSpec source validation failed: %w", err)
	}
	if err := s.Destination.Validate(); err != nil {
		return fmt.Errorf("ArgoCDModuleSpec destination validation failed: %w", err)
	}
	return nil
}

type ArgoCDModule struct {
	BaseResource `yaml:",inline"`
	Spec         ArgoCDModuleSpec `yaml:"spec"`
}

func (module ArgoCDModule) Validate() error {
	return module.Spec.Validate()
}

func (module *ArgoCDModule) RenderSpec(data any) error {
	renderedSpec, err := renderSpecWithTypePreservation(module.Spec, data)
	if err != nil {
		return err
	}

	module.Spec = renderedSpec
	return nil
}

func (module *ArgoCDModule) NewRenderData(config map[string]any, applicationName string) map[string]any {
	return map[string]any{
		"config":          config,
		"applicationName": applicationName,
	}
}
