package types

const ModuleBaseAnnotationKey = "environment.sh"

var ModuleAnnotationKeys = struct {
	Resource,
	BaseModuleConfig,
	ManagerData string
}{
	Resource:         ModuleBaseAnnotationKey + "/resource",
	BaseModuleConfig: ModuleBaseAnnotationKey + "/base-module-config",
	ManagerData:      ModuleBaseAnnotationKey + "/manager-data",
}
