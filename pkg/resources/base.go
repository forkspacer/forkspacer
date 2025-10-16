package resources

import "fmt"

type ObjectMeta struct {
	Name                     string `yaml:"name" json:"name"`
	Version                  string `yaml:"version" json:"version"`
	SupportedOperatorVersion string `yaml:"supportedOperatorVersion" json:"supportedOperatorVersion"`
}

type KindType string

var (
	KindHelmType   KindType = "Helm"
	KindCustomType KindType = "Custom"
)

type TypeMeta struct {
	Kind KindType `yaml:"kind" json:"kind"`
}

type ResourceIndetifier struct {
	Name      string `yaml:"name" json:"name"`
	Namespace string `yaml:"namespace" json:"namespace"`
}

type ConfigMapIndetifier struct {
	ResourceIndetifier `yaml:",inline" json:",inline"`
	Key                string `yaml:"key" json:"key"`
}

func (resource ConfigMapIndetifier) Validate() error {
	if err := resource.ResourceIndetifier.Validate(); err != nil {
		return err
	}
	return nil
}

type SecretIndetifier struct {
	ResourceIndetifier `yaml:",inline" json:",inline"`
	Key                string `yaml:"key" json:"key"`
}

func (resource SecretIndetifier) Validate() error {
	if err := resource.ResourceIndetifier.Validate(); err != nil {
		return err
	}
	return nil
}

func (resource ResourceIndetifier) Validate() error {
	if resource.Name == "" {
		return fmt.Errorf("Resource name cannot be empty")
	}
	return nil
}

// Base interface that all resources must implement
type Resource interface {
	GetKind() KindType
	GetObjectMeta() ObjectMeta
	Validate() error
	ValidateConfig(configInputs map[string]any) error
}

type BaseResource struct {
	TypeMeta   `yaml:",inline" json:",inline"`
	ObjectMeta ObjectMeta   `yaml:"metadata" json:"metadata"`
	Config     []ConfigItem `yaml:"config" json:"config"`
}

func (resource BaseResource) GetKind() KindType {
	return resource.Kind
}

func (resource BaseResource) GetObjectMeta() ObjectMeta {
	return resource.ObjectMeta
}

func (resource BaseResource) ValidateConfig(configInputs map[string]any) error {
	if configInputs == nil {
		configInputs = make(map[string]any)
	}

	validator, err := GenerateConfigInputValidator(resource.Config)
	if err != nil {
		return err
	}

	return validator.ValidateUserInput(configInputs)
}
