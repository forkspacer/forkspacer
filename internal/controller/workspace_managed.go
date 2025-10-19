package controller

import (
	"context"
	"fmt"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// vcluster chart configuration
	vclusterChartRepo    = "https://charts.loft.sh"
	vclusterChartVersion = "0.21.0-alpha.4"
	vclusterChartName    = "vcluster"

	// vcluster kubeconfig secret name
	vclusterKubeconfigSecretName = "vc-kubeconfig"
)

// installVCluster installs a vcluster for the managed workspace
func (r *WorkspaceReconciler) installVCluster(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	// Get in-cluster config to install vcluster
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	// Create Helm service
	helmService, err := services.NewHelmService(
		restConfig,
		func(format string, v ...any) {
			log.V(1).Info(fmt.Sprintf(format, v))
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create Helm service: %w", err)
	}

	// Generate release name for vcluster
	releaseName := fmt.Sprintf("vcluster-%s", workspace.Name)

	// Configure vcluster values
	values := map[string]any{
		"exportKubeConfig": map[string]any{
			"secret": map[string]any{
				"name": vclusterKubeconfigSecretName,
			},
			"server": fmt.Sprintf("https://%s.%s:443", releaseName, workspace.Namespace),
		},
		// Use k3s as default distro (lightweight and fast)
		"vcluster": map[string]any{
			"image": "rancher/k3s:v1.31.3-k3s1",
		},
	}

	// Install vcluster
	log.Info("installing vcluster",
		"workspace", workspace.Name,
		"namespace", workspace.Namespace,
		"release", releaseName)

	version := vclusterChartVersion
	if err := helmService.InstallFromRepository(
		ctx,
		vclusterChartName,
		releaseName,
		workspace.Namespace,
		vclusterChartRepo,
		&version,
		true, // wait for vcluster to be ready
		[]resources.HelmValues{{Raw: values}},
	); err != nil {
		return fmt.Errorf("failed to install vcluster: %w", err)
	}

	log.Info("vcluster installed successfully",
		"workspace", workspace.Name,
		"namespace", workspace.Namespace,
		"release", releaseName)

	return nil
}

// uninstallVCluster removes the vcluster for the managed workspace
func (r *WorkspaceReconciler) uninstallVCluster(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	// Get in-cluster config
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	// Create Helm service
	helmService, err := services.NewHelmService(
		restConfig,
		func(format string, v ...any) {
			log.V(1).Info(fmt.Sprintf(format, v))
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create Helm service: %w", err)
	}

	// Generate release name for vcluster
	releaseName := fmt.Sprintf("vcluster-%s", workspace.Name)

	log.Info("uninstalling vcluster",
		"workspace", workspace.Name,
		"namespace", workspace.Namespace,
		"release", releaseName)

	// Uninstall vcluster
	if err := helmService.UninstallRelease(
		ctx,
		releaseName,
		workspace.Namespace,
		false, // don't remove namespace
		false, // don't remove PVCs
	); err != nil {
		return fmt.Errorf("failed to uninstall vcluster: %w", err)
	}

	log.Info("vcluster uninstalled successfully",
		"workspace", workspace.Name,
		"namespace", workspace.Namespace,
		"release", releaseName)

	return nil
}

// setupManagedWorkspaceConnection configures the workspace connection to use vcluster's kubeconfig
func (r *WorkspaceReconciler) setupManagedWorkspaceConnection(workspace *batchv1.Workspace) {
	// Set connection to use kubeconfig from the vcluster secret
	workspace.Spec.Connection = batchv1.WorkspaceConnection{
		Type: batchv1.WorkspaceConnectionTypeKubeconfig,
		SecretReference: &batchv1.WorkspaceConnectionSecretReference{
			Name:      vclusterKubeconfigSecretName,
			Namespace: workspace.Namespace,
			Key:       "config",
		},
	}
}
