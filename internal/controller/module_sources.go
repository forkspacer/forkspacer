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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
)

func (r *ModuleReconciler) readModuleLocation(ctx context.Context, moduleSource batchv1.ModuleSource) (io.Reader, error) {
	if moduleSource.Raw != nil {
		return r.readModuleFromRaw(moduleSource.Raw)
	}

	if moduleSource.ConfigMap != nil {
		return r.readModuleFromConfigMap(ctx, moduleSource.ConfigMap)
	}

	if moduleSource.HttpURL != nil {
		return r.readModuleFromHTTP(ctx, *moduleSource.HttpURL)
	}

	if moduleSource.Github != nil {
		return r.readModuleFromGithub(ctx, moduleSource.Github)
	}

	if moduleSource.ExistingHelmRelease != nil {
		// Existing Helm releases don't have module definitions to read
		// They are handled specially in installModule
		return nil, nil
	}

	return nil, errors.New("exactly one of 'raw', 'configMap', 'httpURL', 'github', or 'existingHelmRelease' must be specified")
}

func (r *ModuleReconciler) readModuleFromRaw(raw *runtime.RawExtension) (io.Reader, error) {
	// runtime.RawExtension stores data as JSON, convert it to YAML
	yamlData, err := yaml.JSONToYAML(raw.Raw)
	if err != nil {
		return nil, fmt.Errorf("failed to convert raw JSON to YAML: %w", err)
	}
	return bytes.NewReader(yamlData), nil
}

func (r *ModuleReconciler) readModuleFromConfigMap(ctx context.Context, ref *batchv1.ModuleSourceConfigMapRef) (io.Reader, error) {
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      ref.Name,
		Namespace: ref.Namespace,
	}, configMap); err != nil {
		return nil, fmt.Errorf("failed to fetch ConfigMap %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	data, ok := configMap.Data["module.yaml"]
	if !ok {
		return nil, fmt.Errorf("key 'module.yaml' not found in ConfigMap %s/%s", ref.Namespace, ref.Name)
	}

	return bytes.NewReader([]byte(data)), nil
}

func (r *ModuleReconciler) readModuleFromGithub(ctx context.Context, spec *batchv1.ModuleSourceGithubSpec) (io.Reader, error) {
	return nil, errors.New("'github' module location type is not yet supported")
}

func (r *ModuleReconciler) readModuleFromHTTP(ctx context.Context, url string) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request for URL %s: %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch module from HTTP URL %s: %w", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status %d for URL %s", resp.StatusCode, url)
	}

	// Read the entire response body to avoid leaking the connection
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from URL %s: %w", url, err)
	}

	return bytes.NewReader(data), nil
}
