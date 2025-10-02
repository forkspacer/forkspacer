package services

import (
	"context"
	"fmt"
	"time"

	"github.com/forkspacer/forkspacer/pkg/resources"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var (
	argoCDApplicationGVR = schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}
)

type ArgoCDService struct {
	dynamicClient dynamic.Interface
	namespace     string
}

func NewArgoCDService(kubernetesConfig *rest.Config, namespace string) (*ArgoCDService, error) {
	dynamicClient, err := dynamic.NewForConfig(kubernetesConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if namespace == "" {
		namespace = "argocd"
	}

	return &ArgoCDService{
		dynamicClient: dynamicClient,
		namespace:     namespace,
	}, nil
}

func (s *ArgoCDService) CreateApplication(
	ctx context.Context,
	argoCDModule *resources.ArgoCDModule,
) error {
	app := s.buildApplicationManifest(argoCDModule)

	_, err := s.dynamicClient.Resource(argoCDApplicationGVR).
		Namespace(s.namespace).
		Create(ctx, app, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ArgoCD Application: %w", err)
	}

	return nil
}

func (s *ArgoCDService) DeleteApplication(
	ctx context.Context,
	applicationName string,
	cascade bool,
) error {
	// For ArgoCD Applications, we need to handle cascade deletion properly
	// If cascade is false, we need to remove the finalizer first
	if !cascade {
		// Get the application first
		app, err := s.GetApplication(ctx, applicationName)
		if err != nil {
			return fmt.Errorf("failed to get application before deletion: %w", err)
		}

		// Remove the finalizer to orphan resources
		finalizers, found, err := unstructured.NestedStringSlice(app.Object, "metadata", "finalizers")
		if err != nil {
			return fmt.Errorf("failed to get finalizers: %w", err)
		}

		if found && len(finalizers) > 0 {
			// Remove ArgoCD finalizer
			newFinalizers := []string{}
			for _, f := range finalizers {
				if f != "resources-finalizer.argocd.argoproj.io" {
					newFinalizers = append(newFinalizers, f)
				}
			}
			err = unstructured.SetNestedStringSlice(app.Object, newFinalizers, "metadata", "finalizers")
			if err != nil {
				return fmt.Errorf("failed to set finalizers: %w", err)
			}

			// Update the application
			_, err = s.dynamicClient.Resource(argoCDApplicationGVR).
				Namespace(s.namespace).
				Update(ctx, app, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update application finalizers: %w", err)
			}
		}
	}

	// Delete the application
	err := s.dynamicClient.Resource(argoCDApplicationGVR).
		Namespace(s.namespace).
		Delete(ctx, applicationName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete ArgoCD Application: %w", err)
	}

	return nil
}

func (s *ArgoCDService) GetApplication(
	ctx context.Context,
	applicationName string,
) (*unstructured.Unstructured, error) {
	app, err := s.dynamicClient.Resource(argoCDApplicationGVR).
		Namespace(s.namespace).
		Get(ctx, applicationName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ArgoCD Application: %w", err)
	}

	return app, nil
}

func (s *ArgoCDService) WaitForSync(
	ctx context.Context,
	applicationName string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		app, err := s.GetApplication(ctx, applicationName)
		if err != nil {
			return err
		}

		status, found, err := unstructured.NestedMap(app.Object, "status")
		if err != nil || !found {
			time.Sleep(5 * time.Second)
			continue
		}

		syncStatus, found, err := unstructured.NestedString(status, "sync", "status")
		if err != nil || !found {
			time.Sleep(5 * time.Second)
			continue
		}

		healthStatus, _, _ := unstructured.NestedString(status, "health", "status")

		if syncStatus == "Synced" && healthStatus == "Healthy" {
			return nil
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timeout waiting for ArgoCD Application to sync and become healthy")
}

func (s *ArgoCDService) buildApplicationManifest(
	argoCDModule *resources.ArgoCDModule,
) *unstructured.Unstructured {
	spec := argoCDModule.Spec

	app := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]any{
				"name":      spec.ApplicationName,
				"namespace": s.namespace,
			},
			"spec": map[string]any{
				"project":     spec.Project,
				"source":      s.buildSource(spec.Source),
				"destination": s.buildDestination(spec.Destination),
			},
		},
	}

	// Add finalizer if cascade deletion is enabled
	if spec.Cleanup.Cascade {
		finalizers := []any{"resources-finalizer.argocd.argoproj.io"}
		unstructured.SetNestedSlice(app.Object, finalizers, "metadata", "finalizers")
	}

	// Add sync policy if specified
	if spec.SyncPolicy != nil {
		syncPolicy := make(map[string]any)

		if spec.SyncPolicy.Automated != nil {
			syncPolicy["automated"] = map[string]any{
				"prune":      spec.SyncPolicy.Automated.Prune,
				"selfHeal":   spec.SyncPolicy.Automated.SelfHeal,
				"allowEmpty": spec.SyncPolicy.Automated.AllowEmpty,
			}
		}

		if len(spec.SyncPolicy.SyncOptions) > 0 {
			// Convert []string to []interface{} for SetNestedMap compatibility
			syncOptions := make([]any, len(spec.SyncPolicy.SyncOptions))
			for i, opt := range spec.SyncPolicy.SyncOptions {
				syncOptions[i] = opt
			}
			syncPolicy["syncOptions"] = syncOptions
		}

		if spec.SyncPolicy.Retry != nil {
			syncPolicy["retry"] = map[string]any{
				"limit": spec.SyncPolicy.Retry.Limit,
				"backoff": map[string]any{
					"duration":    spec.SyncPolicy.Retry.Backoff.Duration,
					"maxDuration": spec.SyncPolicy.Retry.Backoff.MaxDuration,
				},
			}
			if spec.SyncPolicy.Retry.Backoff.Factor != nil {
				backoff := syncPolicy["retry"].(map[string]any)["backoff"].(map[string]any)
				backoff["factor"] = *spec.SyncPolicy.Retry.Backoff.Factor
			}
		}

		unstructured.SetNestedMap(app.Object, syncPolicy, "spec", "syncPolicy")
	}

	// Add ignore differences if specified
	if len(spec.IgnoreDifferences) > 0 {
		ignoreDiffs := make([]any, len(spec.IgnoreDifferences))
		for i, diff := range spec.IgnoreDifferences {
			ignoreDiff := map[string]any{
				"group": diff.Group,
				"kind":  diff.Kind,
			}
			if diff.Name != "" {
				ignoreDiff["name"] = diff.Name
			}
			if len(diff.JSONPointers) > 0 {
				// Convert []string to []interface{} for unstructured compatibility
				jsonPointers := make([]any, len(diff.JSONPointers))
				for j, ptr := range diff.JSONPointers {
					jsonPointers[j] = ptr
				}
				ignoreDiff["jsonPointers"] = jsonPointers
			}
			ignoreDiffs[i] = ignoreDiff
		}
		unstructured.SetNestedSlice(app.Object, ignoreDiffs, "spec", "ignoreDifferences")
	}

	// Add info if specified
	if len(spec.Info) > 0 {
		info := make([]any, len(spec.Info))
		for i, infoItem := range spec.Info {
			info[i] = map[string]any{
				"name":  infoItem.Name,
				"value": infoItem.Value,
			}
		}
		unstructured.SetNestedSlice(app.Object, info, "spec", "info")
	}

	return app
}

func (s *ArgoCDService) buildSource(source resources.ArgoCDSource) map[string]any {
	src := map[string]any{
		"repoURL":        source.RepoURL,
		"targetRevision": source.TargetRevision,
	}

	if source.Path != nil {
		src["path"] = *source.Path
	}

	if source.Chart != nil {
		src["chart"] = *source.Chart
	}

	if source.Helm != nil {
		helm := make(map[string]any)
		if source.Helm.ReleaseName != "" {
			helm["releaseName"] = source.Helm.ReleaseName
		}
		if source.Helm.Values != "" {
			helm["values"] = source.Helm.Values
		}
		if len(source.Helm.ValueFiles) > 0 {
			// Convert []string to []interface{} for unstructured compatibility
			valueFiles := make([]any, len(source.Helm.ValueFiles))
			for i, vf := range source.Helm.ValueFiles {
				valueFiles[i] = vf
			}
			helm["valueFiles"] = valueFiles
		}
		if len(source.Helm.Parameters) > 0 {
			params := make([]any, len(source.Helm.Parameters))
			for i, param := range source.Helm.Parameters {
				p := map[string]any{
					"name":  param.Name,
					"value": param.Value,
				}
				if param.ForceString {
					p["forceString"] = true
				}
				params[i] = p
			}
			helm["parameters"] = params
		}
		if len(source.Helm.FileParameters) > 0 {
			fileParams := make([]any, len(source.Helm.FileParameters))
			for i, fp := range source.Helm.FileParameters {
				fileParams[i] = map[string]any{
					"name": fp.Name,
					"path": fp.Path,
				}
			}
			helm["fileParameters"] = fileParams
		}
		if len(helm) > 0 {
			src["helm"] = helm
		}
	}

	if source.Kustomize != nil {
		kustomize := make(map[string]any)
		if source.Kustomize.NamePrefix != "" {
			kustomize["namePrefix"] = source.Kustomize.NamePrefix
		}
		if source.Kustomize.NameSuffix != "" {
			kustomize["nameSuffix"] = source.Kustomize.NameSuffix
		}
		if len(source.Kustomize.Images) > 0 {
			// Convert []string to []interface{} for unstructured compatibility
			images := make([]any, len(source.Kustomize.Images))
			for i, img := range source.Kustomize.Images {
				images[i] = img
			}
			kustomize["images"] = images
		}
		if len(source.Kustomize.CommonLabels) > 0 {
			kustomize["commonLabels"] = source.Kustomize.CommonLabels
		}
		if source.Kustomize.Version != "" {
			kustomize["version"] = source.Kustomize.Version
		}
		if len(kustomize) > 0 {
			src["kustomize"] = kustomize
		}
	}

	return src
}

func (s *ArgoCDService) buildDestination(dest resources.ArgoCDDestination) map[string]any {
	destination := map[string]any{
		"namespace": dest.Namespace,
	}

	if dest.Server != nil {
		destination["server"] = *dest.Server
	}

	if dest.Name != nil {
		destination["name"] = *dest.Name
	}

	return destination
}
