package services

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	fileCons "github.com/forkspacer/forkspacer/pkg/constants/file"
	"github.com/forkspacer/forkspacer/pkg/utils"
)

type PVMigrateService struct {
	pvMigrateToolPath string
}

func NewPVMigrateService(pvMigrateToolPath *string) *PVMigrateService {
	if pvMigrateToolPath == nil {
		pvMigrateToolPath = utils.ToPtr(fileCons.PVMigrateCLIToolPath)
	}

	return &PVMigrateService{
		pvMigrateToolPath: *pvMigrateToolPath,
	}
}

func (s *PVMigrateService) NewCMD(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, s.pvMigrateToolPath, args...)
}

func (s *PVMigrateService) RunCMD(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	cmd := s.NewCMD(ctx, args...)

	var stdoutBuff, stderrBuff bytes.Buffer
	cmd.Stdout = &stdoutBuff
	cmd.Stderr = &stderrBuff

	err = cmd.Run()
	stdout = stdoutBuff.String()
	stderr = stderrBuff.String()
	return
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
		"--ignore-mounted",
	}

	_, stderr, err := s.RunCMD(ctx, args...)
	if err != nil {
		return fmt.Errorf("%w: %s", err, stderr)
	}

	return nil
}
