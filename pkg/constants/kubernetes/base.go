package kubernetes

const BaseAnnotationKey = "forkspacer.com"

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
	Source string
}{
	Source: "module.yaml",
}
