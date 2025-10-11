package resources

import (
	"errors"
	"fmt"
)

var _ Resource = CustomModule{}

type CustomModuleRepo struct {
	File      *string             `yaml:"file,omitempty"`
	ConfigMap *ResourceIndetifier `yaml:"configMap,omitempty"`
}

func (resource CustomModuleRepo) Validate() error {
	numSet := 0
	if resource.File != nil {
		numSet++
		if *resource.File == "" {
			return errors.New("file path cannot be empty if specified")
		}
	}
	if resource.ConfigMap != nil {
		numSet++
		if err := resource.ConfigMap.Validate(); err != nil {
			return fmt.Errorf("invalid configMap configuration: %w", err)
		}
	}

	if numSet == 0 {
		return errors.New("either 'file' or 'configMap' must be specified for CustomModuleRepo")
	}
	if numSet > 1 {
		return errors.New("cannot specify both 'file' and 'configMap' for CustomModuleRepo")
	}

	return nil
}

type CustomModuleSpec struct {
	Repo CustomModuleRepo `yaml:"repo"`
}

func (resource CustomModuleSpec) Validate() error {
	if err := resource.Repo.Validate(); err != nil {
		return err
	}
	return nil
}

type CustomModule struct {
	BaseResource `yaml:",inline"`
	Spec         CustomModuleSpec `yaml:"spec"`
}

func (module CustomModule) Validate() error {
	return module.Spec.Validate()
}

func (module *CustomModule) RenderSpec(data any) error {
	renderedSpec, err := renderSpecWithTypePreservation(module.Spec, data)
	if err != nil {
		return err
	}

	module.Spec = renderedSpec
	return nil
}
