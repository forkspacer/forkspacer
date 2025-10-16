package resources

import "fmt"

var _ Resource = CustomModule{}

type CustomModulePermission string

var (
	CustomModulePermissionWorkspace  CustomModulePermission = "workspace"
	CustomModulePermissionController CustomModulePermission = "controller"
)

type CustomModuleSpec struct {
	Image            string                   `yaml:"image" json:"image"`
	ImagePullSecrets []string                 `yaml:"imagePullSecrets" json:"imagePullSecrets"`
	Permissions      []CustomModulePermission `yaml:"permissions" json:"permissions"`
}

func (resource CustomModuleSpec) Validate() error {
	if resource.Image == "" {
		return fmt.Errorf("CustomModuleSpec Image cannot be empty")
	}

	for i, permission := range resource.Permissions {
		if permission != CustomModulePermissionWorkspace && permission != CustomModulePermissionController {
			return fmt.Errorf(
				"CustomModuleSpec Permission at index %d has invalid value '%s', must be either 'workspace' or 'controller'",
				i, permission,
			)
		}
	}

	return nil
}

type CustomModule struct {
	BaseResource `yaml:",inline" json:",inline"`
	Spec         CustomModuleSpec `yaml:"spec" json:"spec"`
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
