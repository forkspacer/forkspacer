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
	Source string
}{
	Source: "module.yaml",
}

var Helm = struct {
	DefaultNamespace,
	ValuesConfigMapKey,
	ChartConfigMapKey,
	ChartGitAuthHTTPSSecretUsernameKey,
	ChartGitAuthHTTPSSecretTokenKey string
}{
	DefaultNamespace:                   "default",
	ValuesConfigMapKey:                 "values",
	ChartConfigMapKey:                  "chart.tgz",
	ChartGitAuthHTTPSSecretUsernameKey: "username",
	ChartGitAuthHTTPSSecretTokenKey:    "token",
}
