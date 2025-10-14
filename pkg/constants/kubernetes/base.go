package kubernetes

const (
	BaseAnnotationKey = "forkspacer.com"
	BaseLabelKey      = "forkspacer.com"
	OperatorNamespace = "forkspacer-system"
)

var ModuleAnnotationKeys = struct {
	Resource,
	BaseModuleConfig,
	ManagerData string
}{
	Resource:         BaseAnnotationKey + "/resource",
	BaseModuleConfig: BaseAnnotationKey + "/base-module-config",
	ManagerData:      BaseAnnotationKey + "/manager-data",
}

var WorkspaceSecretKeys = struct {
	KubeConfig string
}{
	KubeConfig: "kubeconfig",
}

var ModuleConfigMapKeys = struct {
	Source,
	CustomPlugin string
}{
	Source:       "module.yaml",
	CustomPlugin: "plugin",
}

var Helm = struct {
	DefaultNamespace,
	ValuesConfigMapKey string
}{
	DefaultNamespace:   "default",
	ValuesConfigMapKey: "values",
}
