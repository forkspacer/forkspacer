package types

const ModuleBaseAnnotationKey = "forkspacer.com"

var ModuleAnnotationKeys = struct {
	Resource,
	BaseModuleConfig,
	ManagerData string
}{
	Resource:         ModuleBaseAnnotationKey + "/resource",
	BaseModuleConfig: ModuleBaseAnnotationKey + "/base-module-config",
	ManagerData:      ModuleBaseAnnotationKey + "/manager-data",
}
