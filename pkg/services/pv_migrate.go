package services

import (
	"context"
	"os/exec"

	"github.com/forkspacer/forkspacer/pkg/utils"
)

const PVMigrateCLIToolPath = "/usr/local/bin/pv-migrate"

type PVMigrateService struct {
	pvMigrateToolPath string
}

func NewPVMigrateService(pvMigrateToolPath *string) *PVMigrateService {
	if pvMigrateToolPath == nil {
		pvMigrateToolPath = utils.ToPtr(PVMigrateCLIToolPath)
	}

	return &PVMigrateService{
		pvMigrateToolPath: *pvMigrateToolPath,
	}
}

func (s *PVMigrateService) NewCMD(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, s.pvMigrateToolPath, args...)
}

// MigratePVC migrates data from source PVC to destination PVC using pv-migrate CLI
func (s *PVMigrateService) MigratePVC(
	ctx context.Context,
	sourceKubeconfigPath, sourcePVCName, sourcePVCNamespace string,
	destKubeconfigPath, destPVCName, destPVCNamespace string,
) error {
	args := []string{
		"--source-kubeconfig", sourceKubeconfigPath,
		"--source", sourcePVCName,
		"--source-namespace", sourcePVCNamespace,

		"--dest-kubeconfig", destKubeconfigPath,
		"--dest", destPVCName,
		"--dest-namespace", destPVCNamespace,

		"--strategies", "mnt2,svc,lbsvc",
	}

	_, err := s.NewCMD(ctx, args...).CombinedOutput()
	if err != nil {
		return err
	}

	return nil
}
