package manager

import (
	"context"
	"fmt"
	"plugin"

	"github.com/environment.sh/operator/pkg/manager/base"
	"github.com/environment.sh/operator/pkg/resources"
	"github.com/environment.sh/operator/pkg/types"
	"github.com/environment.sh/operator/pkg/utils"
	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ base.IManager = ModuleCustomManager{}

var customMetaDataKeys = struct {
	PluginFilePath string
}{
	PluginFilePath: "pluginFilePath",
}

type ModuleCustomManager struct {
	customManager base.IManager
}

func NewModuleCustomManager(
	ctx context.Context,
	controllerClient client.Client,
	kubernetesConfig *rest.Config,
	customModule *resources.CustomModule,
	config map[string]any,
	metaData base.MetaData,
) (base.IManager, error) {
	if err := customModule.Validate(); err != nil {
		return nil, err
	}

	pluginFile := metaData.DecodeToString(customMetaDataKeys.PluginFilePath)
	if pluginFile == "" {
		fileWritePath := types.BaseDataDir + "/custom-plugins/%x.so"

		if customModule.Spec.Repo.File != nil && *customModule.Spec.Repo.File != "" {
			file, err := utils.WriteHTTPDataToFile(ctx,
				*customModule.Spec.Repo.File,
				fileWritePath,
				false, true,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to write HTTP plugin file from %q: %w", *customModule.Spec.Repo.File, err)
			}
			_ = file.Close()

			pluginFile = file.Name()
		} else if customModule.Spec.Repo.ConfigMap != nil {
			namespace := customModule.Spec.Repo.ConfigMap.Namespace
			if namespace == "" {
				namespace = "default"
			}

			file, err := utils.WriteConfigMapToFile(ctx,
				controllerClient,
				namespace, customModule.Spec.Repo.ConfigMap.Name, resources.CustomPluginConfigMapDataKey,
				fileWritePath,
				false, true,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to write ConfigMap plugin file from %s/%s: %w",
					namespace, customModule.Spec.Repo.ConfigMap.Name, err,
				)
			}
			_ = file.Close()

			pluginFile = file.Name()
		} else {
			return nil, fmt.Errorf("custom module does not specify a plugin file or configmap source")
		}

		metaData[customMetaDataKeys.PluginFilePath] = pluginFile
	}

	customPlugin, err := plugin.Open(pluginFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin file %q: %w", pluginFile, err)
	}

	newManagerSymbol, err := customPlugin.Lookup("NewManager")
	if err != nil {
		return nil, fmt.Errorf("failed to lookup 'NewManager' symbol in plugin: %w", err)
	}

	newManagerFunc, ok := newManagerSymbol.(func(ctx context.Context, logger logr.Logger, kubernetesConfig *rest.Config, config map[string]any) (base.IManager, error)) //nolint:lll
	if !ok {
		return nil, fmt.Errorf(
			"symbol 'NewManager' found in plugin is not of type 'NewCustomManagerT' (%T)",
			newManagerSymbol,
		)
	}

	customManager, err := newManagerFunc(ctx, logf.FromContext(ctx), kubernetesConfig, config)
	if err != nil {
		return nil, err
	}

	return &ModuleCustomManager{
		customManager: customManager,
	}, nil
}

func (m ModuleCustomManager) Install(ctx context.Context, metaData base.MetaData) error {
	return m.customManager.Install(ctx, metaData)
}

func (m ModuleCustomManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	return m.customManager.Uninstall(ctx, metaData)
}

func (m ModuleCustomManager) Sleep(ctx context.Context, metaData base.MetaData) error {
	return m.customManager.Sleep(ctx, metaData)
}

func (m ModuleCustomManager) Resume(ctx context.Context, metaData base.MetaData) error {
	return m.customManager.Resume(ctx, metaData)
}
