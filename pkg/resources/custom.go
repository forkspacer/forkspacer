package resources

var _ Resource = CustomModule{}

type CustomModulePermission string

var (
	CustomModulePermissionWorkspace  CustomModulePermission = "workspace"
	CustomModulePermissionController CustomModulePermission = "controller"
)

type CustomModuleSpec struct {
	Image            string                   `yaml:"image"`
	ImagePullSecrets []string                 `yaml:"imagePullSecrets"`
	Permissions      []CustomModulePermission `yaml:"permissions"`
}

func (resource CustomModuleSpec) Validate() error {
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
