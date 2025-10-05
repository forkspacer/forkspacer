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
	"errors"
	"fmt"
	"io"
	"net/http"

	"sigs.k8s.io/yaml"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
)

func (r *ModuleReconciler) readModuleLocation(moduleSource batchv1.ModuleSource) (io.Reader, error) {
	if moduleSource.Raw != nil {
		// runtime.RawExtension stores data as JSON, convert it to YAML
		yamlData, err := yaml.JSONToYAML(moduleSource.Raw.Raw)
		if err != nil {
			return nil, fmt.Errorf("failed to convert raw JSON to YAML: %w", err)
		}
		return bytes.NewReader(yamlData), nil
	} else if moduleSource.ConfigMap != nil {
		return nil, errors.New("'configMap' module location type is not yet supported")
	} else if moduleSource.HttpURL != nil {
		resp, err := http.Get(*moduleSource.HttpURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch module from HTTP URL %s: %w", *moduleSource.HttpURL, err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP request failed with status %d for URL %s", resp.StatusCode, *moduleSource.HttpURL)
		}
		return resp.Body, nil
	} else if moduleSource.Github != nil {
		return nil, errors.New("'github' module location type is not yet supported")
	} else {
		return nil, errors.New("exactly one of 'raw', 'configMap', 'httpURL', or 'github' must be specified")
	}
}
