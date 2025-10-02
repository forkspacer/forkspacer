package manager

import (
	"context"
	"time"

	"github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	"github.com/go-logr/logr"
)

const defaultArgoCDNamespace = "argocd"

var _ base.IManager = ModuleArgoCDManager{}

var ArgoCDMetaDataKeys = struct {
	ApplicationName string
}{
	ApplicationName: "applicationName",
}

type ModuleArgoCDManager struct {
	argoCDModule    *resources.ArgoCDModule
	argoCDService   *services.ArgoCDService
	applicationName string
	log             logr.Logger
}

func NewModuleArgoCDManager(
	argoCDModule *resources.ArgoCDModule,
	argoCDService *services.ArgoCDService,
	applicationName string,
	logger logr.Logger,
) (base.IManager, error) {
	if err := argoCDModule.Validate(); err != nil {
		return nil, err
	}

	return &ModuleArgoCDManager{
		argoCDModule:    argoCDModule,
		argoCDService:   argoCDService,
		applicationName: applicationName,
		log:             logger,
	}, nil
}

func (m ModuleArgoCDManager) Install(ctx context.Context, metaData base.MetaData) error {
	err := m.argoCDService.CreateApplication(ctx, m.argoCDModule)
	if err != nil {
		return err
	}

	// Wait for sync if automated sync is enabled
	if m.argoCDModule.Spec.SyncPolicy != nil && m.argoCDModule.Spec.SyncPolicy.Automated != nil {
		m.log.Info("Waiting for ArgoCD Application to sync", "application", m.applicationName)
		err = m.argoCDService.WaitForSync(ctx, m.applicationName, 15*time.Minute)
		if err != nil {
			m.log.Error(err, "ArgoCD Application sync timeout", "application", m.applicationName)
			// Don't fail the install, just log the warning
			// The application is created, sync might still complete later
		}
	}

	return nil
}

func (m ModuleArgoCDManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	cascade := m.argoCDModule.Spec.Cleanup.Cascade

	err := m.argoCDService.DeleteApplication(ctx, m.applicationName, cascade)
	if err != nil {
		return err
	}

	return nil
}

func (m ModuleArgoCDManager) Sleep(ctx context.Context, metaData base.MetaData) error {
	// TODO: Implement sleep functionality
	// Options:
	// 1. Disable automated sync temporarily
	// 2. Scale down managed resources (similar to Helm manager)
	// 3. Suspend the ArgoCD Application
	m.log.Info("Sleep not yet implemented for ArgoCD manager")
	return nil
}

func (m ModuleArgoCDManager) Resume(ctx context.Context, metaData base.MetaData) error {
	// TODO: Implement resume functionality
	// Options:
	// 1. Re-enable automated sync
	// 2. Scale back managed resources
	// 3. Resume the ArgoCD Application
	m.log.Info("Resume not yet implemented for ArgoCD manager")
	return nil
}
