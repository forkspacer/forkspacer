package manager

var HelmMetaDataKeys = struct {
	ReplicaHistory,
	InstallOutputs,
	ReleaseName string
}{
	ReplicaHistory: "replicaHistory",
	InstallOutputs: "installOutputs",
	ReleaseName:    "releaseName",
}

var CustomMetaDataKeys = struct {
	PluginFilePath string
}{
	PluginFilePath: "pluginFilePath",
}
