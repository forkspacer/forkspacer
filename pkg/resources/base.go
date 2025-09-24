package resources

import "fmt"

type ObjectMeta struct {
	Name                     string  `yaml:"name"`
	Version                  string  `yaml:"version"`
	SupportedOperatorVersion string  `yaml:"supportedOperatorVersion"`
	Author                   *string `yaml:"author"`
	Description              *string `yaml:"description"`
	Category                 *string `yaml:"category"`
	Image                    *string `yaml:"image"`
	ResourceUsage            struct {
		CPU    string `yaml:"cpu"`
		Memory string `yaml:"memory"`
	} `yaml:"resource_usage"`
}

type KindType string

var (
	KindHelmType   KindType = "Helm"
	KindCustomType KindType = "Custom"
)

type TypeMeta struct {
	Kind KindType `yaml:"kind"`
}

type ResourceIndetifier struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
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
	TypeMeta   `yaml:",inline"`
	ObjectMeta ObjectMeta   `yaml:"metadata"`
	Config     []ConfigItem `yaml:"config"`
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
