package manager

var HelmMetaDataKeys = struct {
	ReplicaHistory,
	InstallOutputs string
}{
	ReplicaHistory: "replicaHistory",
	InstallOutputs: "installOutputs",
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
