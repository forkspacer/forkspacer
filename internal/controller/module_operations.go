/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	managerBase "github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/utils"
)

// ErrWorkspaceNotReady indicates that the workspace exists but is not ready yet
// Controllers should requeue when this error is encountered
type ErrWorkspaceNotReady struct {
	WorkspaceName      string
	WorkspaceNamespace string
	Message            string
}

func (e *ErrWorkspaceNotReady) Error() string {
	return fmt.Sprintf("workspace %s/%s not ready: %s", e.WorkspaceNamespace, e.WorkspaceName, e.Message)
}

func (r *ModuleReconciler) installModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	// Special case: adopt existing Helm release
	if module.Spec.Source.ExistingHelmRelease != nil {
		if err := r.adoptExistingHelmRelease(ctx, module); err != nil {
			return fmt.Errorf("failed to adopt existing Helm release: %w", err)
		}
		return nil
	}

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %v", err)
	}

	// If workspace is not ready yet, return a special error that tells the controller to requeue
	if !workspace.Status.Ready {
		return &ErrWorkspaceNotReady{
			WorkspaceName:      workspace.Name,
			WorkspaceNamespace: workspace.Namespace,
			Message:            "workspace exists but status.ready is false",
		}
	}

	if workspace.Status.Phase != batchv1.WorkspacePhaseReady {
		return &ErrWorkspaceNotReady{
			WorkspaceName:      workspace.Name,
			WorkspaceNamespace: workspace.Namespace,
			Message: fmt.Sprintf("workspace phase is %s, waiting for %s",
				workspace.Status.Phase, batchv1.WorkspacePhaseReady),
		}
	}

	moduleReader, err := r.readModuleLocation(ctx, module.Spec.Source)
	if err != nil {
		return fmt.Errorf("failed to read module location: %v", err)
	}

	moduleData, err := io.ReadAll(moduleReader)
	if err != nil {
		return fmt.Errorf("failed to read module data: %v", err)
	}

	// Create target namespace if requested
	if module.Spec.CreateNamespace {
		if err := r.ensureNamespace(ctx, moduleData, &workspace.Spec.Connection); err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
	}

	annotations := map[string]string{
		kubernetesCons.ModuleAnnotationKeys.Resource: string(moduleData),
	}

	metaData := make(managerBase.MetaData)

	configMap := make(map[string]any)
	if module.Spec.Config != nil && module.Spec.Config.Raw != nil {
		if err := json.Unmarshal(module.Spec.Config.Raw, &configMap); err != nil {
			return fmt.Errorf("failed to unmarshal module config: %v", err)
		}

		annotations[kubernetesCons.ModuleAnnotationKeys.BaseModuleConfig] = string(module.Spec.Config.Raw)
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		moduleData, metaData, configMap,
	); err != nil {
		return err
	} else {
		installErr := iManager.Install(ctx, metaData)

		annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData] = metaData.String()

		patch := client.MergeFrom(module.DeepCopy())
		utils.UpdateMap(&module.Annotations, annotations)
		if err = r.Patch(ctx, module, patch); err != nil {
			log.Error(err,
				"failed to patch module with install annotations",
				"module", module.Name, "namespace", module.Namespace,
			)
		}

		if installErr != nil {
			return fmt.Errorf("failed to install module: %w", installErr)
		}
		return nil
	}
}

func (r *ModuleReconciler) uninstallModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	// Skip uninstallation for adopted releases - just detach
	if module.Annotations != nil && module.Annotations["forkspacer.com/adopted-release"] == "true" {
		log.Info("skipping uninstall for adopted Helm release - only detaching from module",
			"release", module.Annotations["forkspacer.com/release-name"],
		)
		return nil
	}

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		// If workspace doesn't exist (already deleted), skip uninstall
		// This prevents orphaned modules from getting stuck when workspace is deleted
		if k8sErrors.IsNotFound(err) {
			log.Info("workspace not found, skipping uninstall for module",
				"module", module.Name,
				"namespace", module.Namespace,
				"workspace", module.Spec.Workspace.Name)
			return nil
		}
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	// If workspace is being deleted, skip ready/phase checks and proceed with uninstall
	// Both resources are being cleaned up anyway
	if workspace.DeletionTimestamp == nil {
		if !workspace.Status.Ready {
			return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
		}

		switch workspace.Status.Phase {
		case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, batchv1.WorkspacePhaseFailed:
		default:
			return fmt.Errorf(
				"workspace is not in a suitable phase for uninstallation: expected one of ['%s', '%s', '%s'], but got '%s'",
				batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, batchv1.WorkspacePhaseFailed, workspace.Status.Phase,
			)
		}
	}

	utils.InitMap(&module.Annotations)

	resourceAnnotation := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		// If module is in Failed state and has no resource annotation, it means
		// installation never completed, so there's nothing to uninstall
		if module.Status.Phase == batchv1.ModulePhaseFailed {
			log.Info("skipping uninstall for failed module with no resource annotation - nothing was installed",
				"module", module.Name, "namespace", module.Namespace)
			return nil
		}
		return fmt.Errorf("resource definition not found in module annotations for %s/%s", module.Namespace, module.Name)
	}
	moduleData := []byte(resourceAnnotation)

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with uninstall even if metadata parsing fails, as it might not be critical for uninstall
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		moduleData, metaData, configMap,
	); err != nil {
		return err
	} else {
		if err := iManager.Uninstall(ctx, metaData); err != nil {
			return fmt.Errorf("failed to uninstall module: %w", err)
		}
		return nil
	}
}

func (r *ModuleReconciler) sleepModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	if !workspace.Status.Ready {
		return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
	}

	switch workspace.Status.Phase {
	case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated:
	default:
		return fmt.Errorf(
			"workspace is not in a suitable phase for sleeping: expected one of ['%s', '%s'], but got '%s'",
			batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, workspace.Status.Phase,
		)
	}

	utils.InitMap(&module.Annotations)

	resourceAnnotation := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return fmt.Errorf("resource definition not found in module annotations for %s/%s", module.Namespace, module.Name)
	}
	moduleData := []byte(resourceAnnotation)

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with sleep even if metadata parsing fails, as it might not be critical for sleep
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		moduleData, metaData, configMap,
	); err != nil {
		return err
	} else {
		sleepErr := iManager.Sleep(ctx, metaData)

		patch := client.MergeFrom(module.DeepCopy())
		module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData] = metaData.String()
		if err := r.Patch(ctx, module, patch); err != nil {
			log.Error(err,
				"failed to patch module with meta data annotations after sleep",
				"module", module.Name, "namespace", module.Namespace,
			)
		}

		if sleepErr != nil {
			return fmt.Errorf("failed to sleep module: %w", sleepErr)
		}
		return nil
	}
}

func (r *ModuleReconciler) resumeModule(ctx context.Context, module *batchv1.Module) error {
	log := logf.FromContext(ctx)

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace for module %s/%s: %v", module.Namespace, module.Name, err)
	}

	if !workspace.Status.Ready {
		return fmt.Errorf("workspace not ready: workspace %s/%s is not ready", workspace.Namespace, workspace.Name)
	}

	switch workspace.Status.Phase {
	case batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated:
	default:
		return fmt.Errorf(
			"workspace is not in a suitable phase for sleeping: expected one of ['%s', '%s'], but got '%s'",
			batchv1.WorkspacePhaseReady, batchv1.WorkspacePhaseHibernated, workspace.Status.Phase,
		)
	}

	utils.InitMap(&module.Annotations)

	resourceAnnotation := module.Annotations[kubernetesCons.ModuleAnnotationKeys.Resource]
	if resourceAnnotation == "" {
		return fmt.Errorf("resource definition not found in module annotations for %s/%s", module.Namespace, module.Name)
	}
	moduleData := []byte(resourceAnnotation)

	managerData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData]

	metaData := make(managerBase.MetaData)
	if ok && managerData != "" {
		err := metaData.Parse([]byte(managerData))
		if err != nil {
			log.Error(err, "failed to parse manager data for module", "module", module.Name, "namespace", module.Namespace)
			// Proceed with resume even if metadata parsing fails, as it might not be critical for resume
		}
	}

	configMap := make(map[string]any)
	configMapData, ok := module.Annotations[kubernetesCons.ModuleAnnotationKeys.BaseModuleConfig]
	if ok {
		if err := json.Unmarshal([]byte(configMapData), &configMap); err != nil {
			log.Error(err, "failed to unmarshal module config from annotation")
		}
	}

	if iManager, err := r.newManager(ctx,
		module, &workspace.Spec.Connection,
		moduleData, metaData, configMap,
	); err != nil {
		return err
	} else {
		resumeErr := iManager.Resume(ctx, metaData)

		patch := client.MergeFrom(module.DeepCopy())
		module.Annotations[kubernetesCons.ModuleAnnotationKeys.ManagerData] = metaData.String()
		if err := r.Patch(ctx, module, patch); err != nil {
			log.Error(err,
				"failed to patch module with meta data annotations after resume",
				"module", module.Name, "namespace", module.Namespace,
			)
		}

		if resumeErr != nil {
			return fmt.Errorf("failed to resume module: %w", resumeErr)
		}
		return nil
	}
}

func (r *ModuleReconciler) adoptExistingHelmRelease(ctx context.Context, module *batchv1.Module) error {
	ref := module.Spec.Source.ExistingHelmRelease

	workspace, err := r.getWorkspace(ctx, &module.Spec.Workspace)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %v", err)
	}

	// If workspace is not ready yet, return a special error that tells the controller to requeue
	if !workspace.Status.Ready {
		return &ErrWorkspaceNotReady{
			WorkspaceName:      workspace.Name,
			WorkspaceNamespace: workspace.Namespace,
			Message:            "workspace exists but status.ready is false",
		}
	}

	if workspace.Status.Phase != batchv1.WorkspacePhaseReady {
		return &ErrWorkspaceNotReady{
			WorkspaceName:      workspace.Name,
			WorkspaceNamespace: workspace.Namespace,
			Message: fmt.Sprintf("workspace phase is %s, waiting for %s",
				workspace.Status.Phase, batchv1.WorkspacePhaseReady),
		}
	}

	// Create Helm service to verify and retrieve release information
	helmService, err := r.newHelmService(ctx, &workspace.Spec.Connection)
	if err != nil {
		return fmt.Errorf("failed to create Helm service: %w", err)
	}

	// Verify the release exists
	release, err := helmService.GetRelease(ref.Name, ref.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get release: %w", err)
	}

	var chartSource resources.HelmModuleSpecChart
	if ref.ChartSource.Repository != nil {
		chartSource.Repo = &resources.HelmModuleSpecChartRepo{
			URL:       ref.ChartSource.Repository.URL,
			ChartName: ref.ChartSource.Repository.Chart,
			Version:   ref.ChartSource.Repository.Version,
		}
	} else if ref.ChartSource.ConfigMap != nil {
		chartSource.ConfigMap = &resources.ConfigMapIndetifier{
			ResourceIndetifier: resources.ResourceIndetifier{
				Name:      ref.ChartSource.ConfigMap.Name,
				Namespace: ref.ChartSource.ConfigMap.Namespace,
			},
			Key: ref.ChartSource.ConfigMap.Key,
		}
	} else if ref.ChartSource.Git != nil {
		chartSource.Git = &resources.HelmModuleSpecChartGit{
			Repo:     ref.ChartSource.Git.Repo,
			Path:     ref.ChartSource.Git.Path,
			Revision: &ref.ChartSource.Git.Revision,
		}

		if ref.ChartSource.Git.Auth != nil {
			chartSource.Git.Auth = &resources.HelmModuleSpecChartGitAuth{}

			if ref.ChartSource.Git.Auth.HTTPSSecretRef != nil {
				chartSource.Git.Auth.HTTPSSecretRef = &resources.ResourceIndetifier{
					Name:      ref.ChartSource.Git.Auth.HTTPSSecretRef.Name,
					Namespace: "",
				}
			} else {
				return errors.New("unsupported Git chart authentication method, only HTTPSSecretRef is supported")
			}
		}
	} else {
		return errors.New("unsupported chart source type for existing Helm release")
	}

	// Capture all effective values from the release (chart defaults + user-supplied)
	// This ensures forked modules use the exact same configuration as the source
	finalValues := make(map[string]any)

	// Start with chart default values
	if release.Chart != nil && release.Chart.Values != nil {
		finalValues = release.Chart.Values
	}

	// Merge user-supplied values from the release (takes precedence over chart defaults)
	if len(release.Config) > 0 {
		finalValues = chartutil.CoalesceTables(release.Config, finalValues)
	}

	// Finally, apply any additional override values from the Module spec (takes highest precedence)
	if ref.Values != nil && ref.Values.Raw != nil {
		var overrideValues map[string]any
		if err := json.Unmarshal(ref.Values.Raw, &overrideValues); err != nil {
			return fmt.Errorf("failed to unmarshal override values: %w", err)
		}
		finalValues = chartutil.CoalesceTables(overrideValues, finalValues)
	}

	// Build values array - only include if there are actual values
	var helmValues []resources.HelmValues
	if len(finalValues) > 0 {
		helmValues = []resources.HelmValues{
			{
				Raw: finalValues,
			},
		}
	}

	helmResource := resources.HelmModule{
		BaseResource: resources.BaseResource{
			TypeMeta: resources.TypeMeta{
				Kind: resources.KindHelmType,
			},
			ObjectMeta: resources.ObjectMeta{
				Name:                     release.Name,
				Version:                  fmt.Sprintf("%d", release.Version),
				SupportedOperatorVersion: ">= 0.0.0",
			},
			Config: []resources.ConfigItem{},
		},
		Spec: resources.HelmModuleSpec{
			Namespace: release.Namespace,
			Chart:     chartSource,
			Values:    helmValues,
		},
	}

	// helmResourceJSON, err := json.Marshal(helmResource)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal Helm resource to JSON: %w", err)
	// }

	// module.Spec.Source = batchv1.ModuleSource{
	// 	Raw: &runtime.RawExtension{Raw: helmResourceJSON},
	// }
	// if err := r.Update(ctx, module); err != nil {
	// 	return err
	// }

	helmResourceYAML, err := yaml.Marshal(helmResource)
	if err != nil {
		return fmt.Errorf("failed to marshal Helm resource to YAML: %w", err)
	}

	metaData := managerBase.MetaData{
		managerCons.HelmMetaDataKeys.ReleaseName: release.Name,
	}

	annotations := map[string]string{
		kubernetesCons.ModuleAnnotationKeys.Resource:    string(helmResourceYAML),
		kubernetesCons.ModuleAnnotationKeys.ManagerData: metaData.String(),
	}

	patch := client.MergeFrom(module.DeepCopy())
	utils.UpdateMap(&module.Annotations, annotations)
	if err = r.Patch(ctx, module, patch); err != nil {
		return fmt.Errorf("failed to patch module with adoption annotations: %w", err)
	}

	return nil
}

// ensureNamespace creates the target namespace if it doesn't exist
func (r *ModuleReconciler) ensureNamespace(
	ctx context.Context,
	moduleData []byte,
	workspaceConn *batchv1.WorkspaceConnection,
) error {
	log := logf.FromContext(ctx)

	// Parse module to extract namespace
	var targetNamespace string
	configMap := make(map[string]any)

	err := resources.HandleResource(moduleData, &configMap,
		func(helmModule resources.HelmModule) error {
			targetNamespace = helmModule.Spec.Namespace
			return nil
		},
		func(customModule resources.CustomModule) error {
			// Custom modules don't have a namespace field
			// Skip namespace creation for custom modules
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to parse module to extract namespace: %w", err)
	}

	if targetNamespace == "" {
		return fmt.Errorf("module does not specify a target namespace")
	}

	// Get kubeconfig for target cluster
	apiConfig, err := NewAPIConfig(ctx, workspaceConn, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get API config: %w", err)
	}

	// Create Kubernetes client for target cluster
	restConfig, err := clientcmd.BuildConfigFromKubeconfigGetter("", func() (*clientcmdapi.Config, error) {
		return apiConfig, nil
	})
	if err != nil {
		return fmt.Errorf("failed to build REST config: %w", err)
	}

	targetClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Check if namespace exists
	_, err = targetClient.CoreV1().Namespaces().Get(ctx, targetNamespace, metav1.GetOptions{})
	if err == nil {
		// Namespace already exists
		log.V(1).Info("namespace already exists", "namespace", targetNamespace)
		return nil
	}

	if !k8sErrors.IsNotFound(err) {
		return fmt.Errorf("failed to check namespace existence: %w", err)
	}

	// Create namespace
	log.Info("creating namespace", "namespace", targetNamespace)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNamespace,
		},
	}
	_, err = targetClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if k8sErrors.IsAlreadyExists(err) {
			// Race condition - namespace was created by another process
			log.V(1).Info("namespace was created concurrently", "namespace", targetNamespace)
			return nil
		}
		return fmt.Errorf("failed to create namespace %s: %w", targetNamespace, err)
	}

	log.Info("namespace created successfully", "namespace", targetNamespace)
	return nil
}
