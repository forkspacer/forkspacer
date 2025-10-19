package controller

import (
	"context"
	"fmt"
	"os"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	"github.com/forkspacer/forkspacer/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// getVClusterNamespace returns the dedicated namespace name for a workspace's vcluster
func getVClusterNamespace(workspace *batchv1.Workspace) string {
	return fmt.Sprintf("forkspacer-%s", workspace.Name)
}

// installVCluster installs a vcluster for the managed workspace
func (r *WorkspaceReconciler) installVCluster(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	// Get vcluster configuration
	chartRepo, chartVersion, chartName, secretNamePrefix := getVClusterConfig()

	// Get dedicated namespace for this vcluster
	vclusterNamespace := getVClusterNamespace(workspace)

	// Create dedicated namespace if it doesn't exist
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: vclusterNamespace,
		},
	}
	if err := r.Create(ctx, ns); err != nil {
		if !k8sErrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create namespace %s: %w", vclusterNamespace, err)
		}
		log.Info("namespace already exists", "namespace", vclusterNamespace)
	} else {
		log.Info("created dedicated namespace", "namespace", vclusterNamespace)
	}

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
			"server": fmt.Sprintf("https://%s.%s:443", releaseName, vclusterNamespace),
		},
	}

	// Install vcluster
	log.Info("installing vcluster",
		"workspace", workspace.Name,
		"vcluster_namespace", vclusterNamespace,
		"release", releaseName,
		"chartRepo", chartRepo,
		"chartVersion", chartVersion)

	if err := helmService.InstallFromRepository(
		ctx,
		chartName,
		releaseName,
		vclusterNamespace,
		chartRepo,
		&chartVersion,
		true, // wait for vcluster to be ready
		[]resources.HelmValues{{Raw: values}},
		"", // no username for public vcluster repo
		"", // no password for public vcluster repo
	); err != nil {
		return fmt.Errorf("failed to install vcluster: %w", err)
	}

	log.Info("vcluster installed successfully",
		"workspace", workspace.Name,
		"vcluster_namespace", vclusterNamespace,
		"release", releaseName)

	return nil
}

// uninstallVCluster removes the vcluster for the managed workspace
func (r *WorkspaceReconciler) uninstallVCluster(ctx context.Context, workspace *batchv1.Workspace) error {
	log := logf.FromContext(ctx)

	// Get dedicated namespace for this vcluster
	vclusterNamespace := getVClusterNamespace(workspace)

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
		"vcluster_namespace", vclusterNamespace,
		"release", releaseName)

	// Uninstall vcluster
	if err := helmService.UninstallRelease(
		ctx,
		releaseName,
		vclusterNamespace,
		false, // don't remove namespace (we'll delete it manually)
		false, // don't remove PVCs
	); err != nil {
		return fmt.Errorf("failed to uninstall vcluster: %w", err)
	}

	log.Info("vcluster uninstalled successfully",
		"workspace", workspace.Name,
		"vcluster_namespace", vclusterNamespace,
		"release", releaseName)

	// Delete the dedicated namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: vclusterNamespace,
		},
	}
	if err := r.Delete(ctx, ns); err != nil {
		if !k8sErrors.IsNotFound(err) {
			log.Error(err, "failed to delete vcluster namespace", "namespace", vclusterNamespace)
			return fmt.Errorf("failed to delete namespace %s: %w", vclusterNamespace, err)
		}
	} else {
		log.Info("deleted vcluster namespace", "namespace", vclusterNamespace)
	}

	return nil
}
