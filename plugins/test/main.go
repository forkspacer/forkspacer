package main

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/environment.sh/operator/pkg/manager/base"
)

var _ base.NewCustomManagerT = NewManager

func NewManager(
	ctx context.Context,
	logger logr.Logger,
	kubernetesConfig *rest.Config,
	config map[string]any,
) (base.IManager, error) {
	kubernetesClient, err := kubernetes.NewForConfig(kubernetesConfig)
	if err != nil {
		return nil, err
	}

	return TestPlugin{log: logger, kubernetesClient: kubernetesClient}, nil
}

type TestPlugin struct {
	log              logr.Logger
	kubernetesClient *kubernetes.Clientset
}

func (plugin TestPlugin) Install(ctx context.Context, metaData base.MetaData) error {
	plugin.log.Info("Install 'Test' custom plugin")
	return nil
}

func (plugin TestPlugin) Uninstall(ctx context.Context, metaData base.MetaData) error {
	plugin.log.Info("Uninstall 'Test' custom plugin")
	return nil
}

func (plugin TestPlugin) Sleep(ctx context.Context, metaData base.MetaData) error {
	plugin.log.Info("Sleep 'Test' custom plugin")
	return nil
}

func (plugin TestPlugin) Resume(ctx context.Context, metaData base.MetaData) error {
	plugin.log.Info("Resume 'Test' custom plugin")
	return nil
}

func main() {}
