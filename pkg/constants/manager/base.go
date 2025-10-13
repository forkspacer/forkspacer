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
	RunnerPodName,
	RunnerPodUniqueLabel,
	RunnerServiceName,
	InnerMetaData string
}{
	RunnerPodName:        "runnerPodName",
	RunnerPodUniqueLabel: "runnerPodUniqueLabel",
	RunnerServiceName:    "runnerServiceName",
	InnerMetaData:        "innerMetaData",
}
