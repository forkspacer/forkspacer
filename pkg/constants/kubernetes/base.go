package kubernetes

const (
	BaseAnnotationKey = "forkspacer.com"
	BaseLabelKey      = "forkspacer.com"
	OperatorNamespace = "forkspacer-system"
)

var ModuleAnnotationKeys = struct {
	ManagerData string
}{
	ManagerData: BaseAnnotationKey + "/manager-data",
}

var Helm = struct {
	ChartGitAuthHTTPSSecretUsernameKey,
	ChartGitAuthHTTPSSecretTokenKey,
	ChartRepoAuthSecretUsernameKey,
	ChartRepoAuthSecretPasswordKey string
}{
	ChartGitAuthHTTPSSecretUsernameKey: "username",
	ChartGitAuthHTTPSSecretTokenKey:    "token",
	ChartRepoAuthSecretUsernameKey:     "username",
	ChartRepoAuthSecretPasswordKey:     "password",
}
