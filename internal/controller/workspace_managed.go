package controller

import (
	"context"
	"fmt"
	"os"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/resources"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// getVClusterConfig returns vcluster configuration from environment variables with fallback defaults
func getVClusterConfig() (chartRepo, chartVersion, chartName, secretName string) {
	chartRepo = os.Getenv("VCLUSTER_CHART_REPO")
	if chartRepo == "" {
		chartRepo = "https://charts.loft.sh"
	}

	chartVersion = os.Getenv("VCLUSTER_CHART_VERSION")
	if chartVersion == "" {
		chartVersion = "0.21.0-alpha.4"
	}

	chartName = os.Getenv("VCLUSTER_CHART_NAME")
	if chartName == "" {
		chartName = "vcluster"
	}

	secretName = os.Getenv("VCLUSTER_KUBECONFIG_SECRET_NAME")
	if secretName == "" {
		secretName = "vc-kubeconfig"
	}

	return
}

// installVCluster installs a vcluster for the managed workspace
func (r *WorkspaceReconciler) installVCluster(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	// Get vcluster configuration
	chartRepo, chartVersion, chartName, secretNamePrefix := getVClusterConfig()

	// Use controller's own connection (works both locally and in-cluster)
	controllerConnection := &batchv1.WorkspaceConnection{
		Type: batchv1.WorkspaceConnectionTypeLocal,
	}

	// Create Helm service using controller's connection
	helmService, err := NewHelmService(ctx, controllerConnection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to create Helm service: %w", err)
	}

	// Generate release name for vcluster
	releaseName := fmt.Sprintf("forkspacer-%s", workspace.Name)

	// Generate unique secret name per workspace to avoid collisions
	secretName := fmt.Sprintf("%s-%s", secretNamePrefix, workspace.Name)

	// Configure vcluster values
	values := map[string]any{
		"exportKubeConfig": map[string]any{
			"secret": map[string]any{
				"name": secretName,
			},
			"server": fmt.Sprintf("https://%s.%s:443", releaseName, workspace.Namespace),
		},
	}

	// Install vcluster
	log.Info("installing vcluster",
		"workspace", workspace.Name,
		"namespace", workspace.Namespace,
		"release", releaseName,
		"chartRepo", chartRepo,
		"chartVersion", chartVersion)

	if err := helmService.InstallFromRepository(
		ctx,
		chartName,
		releaseName,
		workspace.Namespace,
		chartRepo,
		&chartVersion,
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

	// Use controller's own connection (works both locally and in-cluster)
	controllerConnection := &batchv1.WorkspaceConnection{
		Type: batchv1.WorkspaceConnectionTypeLocal,
	}

	// Create Helm service using controller's connection
	helmService, err := NewHelmService(ctx, controllerConnection, r.Client)
	if err != nil {
		return fmt.Errorf("failed to create Helm service: %w", err)
	}

	// Generate release name for vcluster
	releaseName := fmt.Sprintf("forkspacer-%s", workspace.Name)

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
	// Get vcluster configuration
	_, _, _, secretNamePrefix := getVClusterConfig()

	// Generate unique secret name per workspace to avoid collisions
	secretName := fmt.Sprintf("%s-%s", secretNamePrefix, workspace.Name)

	// Set connection to use kubeconfig from the vcluster secret
	workspace.Spec.Connection = batchv1.WorkspaceConnection{
		Type: batchv1.WorkspaceConnectionTypeKubeconfig,
		SecretReference: &batchv1.WorkspaceConnectionSecretReference{
			Name:      secretName,
			Namespace: workspace.Namespace,
			Key:       "config",
		},
	}
}
