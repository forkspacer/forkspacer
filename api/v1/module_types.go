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

package v1

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"text/template"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:validation:Enum=ready;installing;uninstalling;sleeping;sleeped;resuming;failed
type ModulePhaseType string

const (
	ModulePhaseReady        ModulePhaseType = "ready"
	ModulePhaseInstalling   ModulePhaseType = "installing"
	ModulePhaseUninstalling ModulePhaseType = "uninstalling"
	ModulePhaseSleeping     ModulePhaseType = "sleeping"
	ModulePhaseSleeped      ModulePhaseType = "sleeped"
	ModulePhaseResuming     ModulePhaseType = "resuming"
	ModulePhaseFailed       ModulePhaseType = "failed"
)

// ModuleStatus defines the observed state of Module.
type ModuleStatus struct {
	// conditions represent the current state of the Module resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	Phase ModulePhaseType `json:"phase"`

	// +optional
	Source string `json:"source,omitempty"`

	// +optional
	LastActivity *metav1.Time `json:"lastActivity,omitempty"`

	// +optional
	Message *string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mo
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Phase,type=string
// +kubebuilder:printcolumn:JSONPath=".status.source",name=Source,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.workspace.namespace",name=Workspace NS,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.workspace.name",name=Workspace Name,type=string
// +kubebuilder:printcolumn:JSONPath=".status.lastActivity",name=Last Activity,type=string,format=date-time
// +kubebuilder:printcolumn:JSONPath=".status.message",name=Message,type=string

// Module is the Schema for the modules API
type Module struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +optional
	Config []ConfigItem `json:"config,omitempty,omitzero"`

	// spec defines the desired state of Module
	// +required
	Spec ModuleSpec `json:"spec"`

	// status defines the observed state of Module
	// +optional
	Status ModuleStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ModuleList contains a list of Module
type ModuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Module `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Module{}, &ModuleList{})
}

type ModuleSpec struct {
	// +optional
	Helm *ModuleSpecHelm `json:"helm,omitempty"`

	// +optional
	Custom *ModuleSpecCustom `json:"custom,omitempty"`

	// +required
	Workspace ModuleWorkspaceReference `json:"workspace"`

	// +optional
	Config *runtime.RawExtension `json:"config,omitempty"`

	// +kubebuilder:default=false
	Hibernated bool `json:"hibernated"`
}

type ModuleWorkspaceReference struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
}

// +kubebuilder:validation:Enum=workspace;controller
type CustomModulePermissionType string

var (
	CustomModulePermissionTypeWorkspace  CustomModulePermissionType = "workspace"
	CustomModulePermissionTypeController CustomModulePermissionType = "controller"
)

type ModuleSpecCustom struct {
	// +required
	Image string `json:"image"`

	// +optional
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`

	// +optional
	Permissions []CustomModulePermissionType `json:"permissions,omitempty"`
}

type ModuleSpecHelmChartRepoAuth struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

type ModuleSpecHelmChartRepo struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// +required
	// +kubebuilder:validation:MinLength=1
	Chart string `json:"chart"`

	// +optional
	Version *string `json:"version,omitempty"`

	// Authentication credentials for private chart repositories
	// +optional
	Auth *ModuleSpecHelmChartRepoAuth `json:"auth,omitempty"`
}

type ModuleSpecHelmChartConfigMap struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// +kubebuilder:default=chart.tgz
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

type ModuleSpecHelmChartGitAuthSecret struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

type ModuleSpecHelmChartGitAuth struct {
	// Reference to a Secret containing Git credentials
	// username and token fields
	// +optional
	HTTPSSecretRef *ModuleSpecHelmChartGitAuthSecret `json:"httpsSecretRef"`

	// SShSecretRef `json:"sshSecretRef"` // TODO
}

type ModuleSpecHelmChartGit struct {
	// Repository URL (https or ssh)
	// +required
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// Path to the chart directory containing Chart.yaml
	// +kubebuilder:default="/"
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Git revision (branch, tag)
	// +kubebuilder:default=main
	Revision string `json:"revision"`

	// Authentication credentials for private repositories
	// +optional
	Auth *ModuleSpecHelmChartGitAuth `json:"auth,omitempty"`
}

type ModuleSpecHelmChart struct {
	// +optional
	Repo *ModuleSpecHelmChartRepo `json:"repo,omitempty"`

	// +optional
	ConfigMap *ModuleSpecHelmChartConfigMap `json:"configMap,omitempty"`

	// +optional
	Git *ModuleSpecHelmChartGit `json:"git,omitempty"`
}

type ModuleSpecHelmExistingRelease struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

type ModuleSpecHelmValuesConfigMap struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// +kubebuilder:default=values.yaml
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

type ModuleSpecHelmValues struct {
	// +optional
	File *string `json:"file,omitempty"`

	// +optional
	ConfigMap *ModuleSpecHelmValuesConfigMap `json:"configMap,omitempty"`

	// +optional
	Raw *runtime.RawExtension `json:"raw,omitempty"`
}

type ModuleSpecHelmOutputValueFromSecret struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// +required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

type ModuleSpecHelmOutputValueFrom struct {
	// +optional
	Secret *ModuleSpecHelmOutputValueFromSecret `json:"secret,omitempty"`
}

type ModuleSpecHelmOutput struct {
	// +required
	Name string `json:"name"`

	// +optional
	Value *apiextensionsv1.JSON `json:"value,omitempty"`

	// +optional
	ValueFrom *ModuleSpecHelmOutputValueFrom `json:"valueFrom,omitempty"`
}

type ModuleSpecHelmCleanup struct {
	// +kubebuilder:default=false
	RemoveNamespace bool `json:"removeNamespace"`

	// +kubebuilder:default=false
	RemovePVCs bool `json:"removePVCs"`
}

type ModuleSpecHelmMigration struct {
	// +optional
	PVCs []string `json:"pvcs,omitempty"`

	// +optional
	ConfigMaps []string `json:"configMaps,omitempty"`

	// +optional
	Secrets []string `json:"secrets,omitempty"`
}

type ModuleSpecHelm struct {
	// +optional
	ExistingRelease *ModuleSpecHelmExistingRelease `json:"existingRelease,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern="^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*)?$"
	ReleaseName *string `json:"releaseName,omitempty"`

	// +required
	Chart ModuleSpecHelmChart `json:"chart"`

	// Namespace for the Helm release.
	// IMPORTANT: When programmatically accessing this field, always use ModuleSpecHelm.GetNamespace()
	// instead of accessing this field directly. The getter returns the effective namespace:
	// - If ExistingRelease is set, it returns ExistingRelease.Namespace
	// - Otherwise, it returns this Namespace field
	// This field supports Go templates (e.g., "{{ .releaseName }}", "dev-{{ .moduleName }}")
	// +kubebuilder:default="default"
	Namespace string `json:"namespace"`

	// +optional
	Values []ModuleSpecHelmValues `json:"values,omitempty"`

	// +optional
	Outputs []ModuleSpecHelmOutput `json:"outputs,omitempty"`

	// +kubebuilder:default={removeNamespace: false, removePVCs: false}
	Cleanup ModuleSpecHelmCleanup `json:"cleanup"`

	// +optional
	Migration ModuleSpecHelmMigration `json:"migration"`
}

// GetNamespace returns the effective Helm namespace
// If ExistingRelease is set, returns its namespace (for adopted releases)
// Otherwise returns the configured Namespace (for new/forked releases)
func (h *ModuleSpecHelm) GetNamespace() string {
	if h.ExistingRelease != nil {
		return h.ExistingRelease.Namespace
	}
	return h.Namespace
}

// GetReleaseName returns the effective Helm release name
// If ExistingRelease is set, returns its release name (for adopted releases)
// Otherwise returns the configured release name (for new/forked releases)
func (h *ModuleSpecHelm) GetReleaseName() string {
	if h.ExistingRelease != nil {
		return h.ExistingRelease.Name
	}

	return *h.ReleaseName
}

type ConfigItemSpecInteger struct {
	// +kubebuilder:default=false
	Required bool `json:"required"`

	// +kubebuilder:default=0
	Default int `json:"default"`

	// +optional
	Min *int `json:"min,omitempty"`

	// +optional
	Max *int `json:"max,omitempty"`

	// +kubebuilder:default=true
	Editable bool `json:"editable"`
}

func (spec ConfigItemSpecInteger) Validate(input *any) (int, error) {
	if input == nil {
		if spec.Required {
			return spec.Default, fmt.Errorf("a value is required for this integer field")
		}

		return spec.Default, nil
	}

	if !spec.Editable {
		return spec.Default, fmt.Errorf("this integer field is not editable")
	}

	var val int
	switch v := (*input).(type) {
	case int:
		val = v
	case float64:
		val = int(v)
	default:
		return spec.Default, fmt.Errorf("input is not an integer")
	}

	if spec.Min != nil && val < *spec.Min {
		return spec.Default, fmt.Errorf("value %d is less than minimum %d", val, *spec.Min)
	}

	if spec.Max != nil && val > *spec.Max {
		return spec.Default, fmt.Errorf("value %d is greater than maximum %d", val, *spec.Max)
	}

	return val, nil
}

type ConfigItemSpecBoolean struct {
	// +kubebuilder:default=false
	Required bool `json:"required"`

	// +kubebuilder:default=false
	Default bool `json:"default"`

	// +kubebuilder:default=true
	Editable bool `json:"editable"`
}

func (spec ConfigItemSpecBoolean) Validate(input *any) (bool, error) {
	if input == nil {
		if spec.Required {
			return spec.Default, fmt.Errorf("a value is required for this boolean field")
		}

		return spec.Default, nil
	}

	if !spec.Editable {
		return spec.Default, fmt.Errorf("this boolean field is not editable")
	}

	val, ok := (*input).(bool)
	if !ok {
		return spec.Default, fmt.Errorf("input is not a boolean")
	}

	return val, nil
}

type ConfigItemSpecString struct {
	// +kubebuilder:default=false
	Required bool `json:"required"`

	// +kubebuilder:default=""
	Default string `json:"default"`

	// +optional
	Regex *string `json:"regex,omitempty"`

	// +kubebuilder:default=true
	Editable bool `json:"editable"`
}

func (spec ConfigItemSpecString) Validate(input *any) (string, error) {
	if input == nil {
		if spec.Required {
			return spec.Default, fmt.Errorf("a value is required for this string field")
		}

		return spec.Default, nil
	}

	if !spec.Editable {
		return spec.Default, fmt.Errorf("this string field is not editable")
	}

	val, ok := (*input).(string)
	if !ok {
		return spec.Default, fmt.Errorf("input is not a string")
	}

	if spec.Regex != nil {
		var re *regexp.Regexp
		re, err := regexp.Compile(*spec.Regex)
		if err != nil {
			return spec.Default, fmt.Errorf("invalid regex pattern: %w", err)
		}

		if !re.MatchString(val) {
			return spec.Default, fmt.Errorf("value '%s' does not match the required regex pattern", val)
		}
	}

	return val, nil
}

type ConfigItemSpecOption struct {
	// +kubebuilder:default=false
	Required bool `yaml:"required" json:"required"`

	// +kubebuilder:default=""
	Default string `yaml:"default" json:"default"`

	// +required
	// +kubebuilder:validation:MinItems=1
	Values []string `yaml:"values" json:"values"`

	// +kubebuilder:default=true
	Editable bool `yaml:"editable" json:"editable"`
}

func (spec ConfigItemSpecOption) Validate(input *any) (string, error) {
	if input == nil {
		if spec.Required {
			return spec.Default, fmt.Errorf("a value is required for this option field")
		}

		return spec.Default, nil
	}

	if !spec.Editable {
		return spec.Default, fmt.Errorf("this option field is not editable")
	}

	val, ok := (*input).(string)
	if !ok {
		return spec.Default, fmt.Errorf("input is not a string")
	}

	if !slices.Contains(spec.Values, val) {
		return spec.Default, fmt.Errorf("invalid value '%s', must be one of %v", val, spec.Values)
	}

	return val, nil
}

type ConfigItemSpecMultipleOptions struct {
	// +kubebuilder:default=false
	Required bool `yaml:"required" json:"required"`

	// +optional
	Default []string `yaml:"default" json:"default,omitempty"`

	// +required
	// +kubebuilder:validation:MinItems=1
	Values []string `yaml:"values" json:"values"`

	// +optional
	Min *int `json:"min,omitempty"`

	// +optional
	Max *int `json:"max,omitempty"`

	// +kubebuilder:default=true
	Editable bool `yaml:"editable" json:"editable"`
}

func (spec ConfigItemSpecMultipleOptions) Validate(input *any) ([]string, error) {
	if input == nil {
		if spec.Required {
			return spec.Default, fmt.Errorf("a value is required for this multiple options field")
		}

		return spec.Default, nil
	}

	if !spec.Editable {
		return spec.Default, fmt.Errorf("this multiple options field is not editable")
	}

	val, ok := (*input).([]string)
	if !ok {
		return spec.Default, fmt.Errorf("input is not an string array")
	}

	if len(val) == 0 && spec.Required {
		return spec.Default, fmt.Errorf("a value is required for this multiple options field")
	}

	for _, item := range val {
		if !slices.Contains(spec.Values, item) {
			return spec.Default, fmt.Errorf("invalid option '%s', must be one of %v", item, spec.Values)
		}
	}

	return val, nil
}

type ConfigItem struct {
	// +required
	Name string `json:"name"`

	// +required
	Alias string `json:"alias"`

	// +optional
	Integer *ConfigItemSpecInteger `json:"integer,omitempty"`

	// +optional
	Boolean *ConfigItemSpecBoolean `json:"boolean,omitempty"`

	// +optional
	String *ConfigItemSpecString `json:"string,omitempty"`

	// +optional
	Option *ConfigItemSpecOption `json:"option,omitempty"`

	// +optional
	MultipleOptions *ConfigItemSpecMultipleOptions `json:"multipleOptions,omitempty"`
}

// generateBase62String generates a random string of specified length using base62 charset
func generateBase62String(length uint) (string, error) {
	const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	result := make([]byte, length)

	for i := range length {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(base62Chars))))
		if err != nil {
			return "", err
		}
		result[i] = base62Chars[num.Int64()]
	}

	return string(result), nil
}

// getTemplateFuncMap returns a template.FuncMap with custom functions for rendering specs
func getTemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"randBase62": func(length uint) string {
			str, err := generateBase62String(length)
			if err != nil {
				return ""
			}
			return str
		},
	}
}

// RenderHelmSpec renders the Helm spec with config values and releaseName
func (m *Module) RenderHelmSpec() (*ModuleSpecHelm, error) {
	if m.Spec.Helm == nil {
		return nil, fmt.Errorf("module does not have a Helm spec")
	}

	// Get and validate config
	outputConfig, err := m.GetValidatedConfig()
	if err != nil {
		return nil, err
	}

	// Get or generate release name
	releaseName := m.Spec.Helm.GetReleaseName()

	// First, get the effective namespace (from ExistingRelease or Helm.Namespace)
	// Then render it with releaseName and config
	effectiveNamespace := m.Spec.Helm.GetNamespace()
	renderedNamespace, err := renderSpecWithTypePreservation(
		effectiveNamespace,
		map[string]any{
			"config":      outputConfig,
			"releaseName": releaseName,
		},
		getTemplateFuncMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to render Helm namespace: %w", err)
	}

	// Then render the entire spec with releaseName, config, and the rendered namespace
	renderedSpec, err := renderSpecWithTypePreservation(
		m.Spec.Helm,
		map[string]any{
			"config":      outputConfig,
			"releaseName": releaseName,
			"namespace":   renderedNamespace,
		},
		getTemplateFuncMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to render Helm spec: %w", err)
	}

	return renderedSpec, nil
}

// RenderCustomSpec renders the Custom spec with config values
func (m *Module) RenderCustomSpec() (*ModuleSpecCustom, error) {
	if m.Spec.Custom == nil {
		return nil, fmt.Errorf("module does not have a Custom spec")
	}

	// Get and validate config
	outputConfig, err := m.GetValidatedConfig()
	if err != nil {
		return nil, err
	}

	// Render the spec
	renderedSpec, err := renderSpecWithTypePreservation(
		m.Spec.Custom,
		map[string]any{
			"config": outputConfig,
		},
		getTemplateFuncMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to render Custom spec: %w", err)
	}

	return renderedSpec, nil
}

// GetValidatedConfig validates and processes the module config
func (m *Module) GetValidatedConfig() (map[string]any, error) {
	var inputConfig map[string]any
	if m.Spec.Config != nil && m.Spec.Config.Raw != nil {
		if err := json.Unmarshal(m.Spec.Config.Raw, &inputConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal module config: %w", err)
		}
	}

	outputConfig := make(map[string]any)
	for _, config := range m.Config {
		var val *any
		inputVal, ok := inputConfig[config.Alias]
		if ok {
			val = &inputVal
		}

		var err error
		if config.Integer != nil {
			outputConfig[config.Alias], err = config.Integer.Validate(val)
			if err != nil {
				return nil, fmt.Errorf("failed to validate integer config for alias %q: %w", config.Alias, err)
			}
		} else if config.Boolean != nil {
			outputConfig[config.Alias], err = config.Boolean.Validate(val)
			if err != nil {
				return nil, fmt.Errorf("failed to validate boolean config for alias %q: %w", config.Alias, err)
			}
		} else if config.String != nil {
			outputConfig[config.Alias], err = config.String.Validate(val)
			if err != nil {
				return nil, fmt.Errorf("failed to validate string config for alias %q: %w", config.Alias, err)
			}
		} else if config.Option != nil {
			outputConfig[config.Alias], err = config.Option.Validate(val)
			if err != nil {
				return nil, fmt.Errorf("failed to validate option config for alias %q: %w", config.Alias, err)
			}
		} else if config.MultipleOptions != nil {
			outputConfig[config.Alias], err = config.MultipleOptions.Validate(val)
			if err != nil {
				return nil, fmt.Errorf("failed to validate multiple options config for alias %q: %w", config.Alias, err)
			}
		} else {
			return nil, fmt.Errorf("unknown config type for alias %q", config.Alias)
		}
	}

	return outputConfig, nil
}

func renderSpecWithTypePreservation[T any](spec T, data any, funcMap template.FuncMap) (T, error) {
	var zero T

	// First, convert spec to JSON to properly handle apiextensionsv1.JSON types
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return zero, err
	}

	var specInterface any
	if err = json.Unmarshal(specBytes, &specInterface); err != nil {
		return zero, err
	}

	// Recursively process the interface structure
	renderedInterface, err := renderInterface(specInterface, data, funcMap)
	if err != nil {
		return zero, err
	}

	// Marshal back to JSON and unmarshal to struct
	renderedBytes, err := json.Marshal(renderedInterface)
	if err != nil {
		return zero, err
	}

	var renderedSpec T
	if err = json.Unmarshal(renderedBytes, &renderedSpec); err != nil {
		return zero, err
	}

	return renderedSpec, nil
}

func renderInterface(v any, data any, funcMap template.FuncMap) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			rendered, err := renderInterface(v, data, funcMap)
			if err != nil {
				return nil, err
			}
			result[k] = rendered
		}
		return result, nil
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			rendered, err := renderInterface(v, data, funcMap)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil
	case string:
		// Only process strings that contain template syntax
		if bytes.Contains([]byte(val), []byte("{{")) && bytes.Contains([]byte(val), []byte("}}")) {
			tmpl := template.New("")
			if funcMap != nil {
				tmpl = tmpl.Funcs(funcMap)
			}

			tmpl, err := tmpl.Parse(val)
			if err != nil {
				return nil, err
			}

			var buf bytes.Buffer
			if err = tmpl.Execute(&buf, data); err != nil {
				return nil, err
			}

			rendered := buf.String()

			// Try to parse as JSON to preserve types (numbers, booleans, etc.)
			var parsedValue any
			if err := json.Unmarshal([]byte(rendered), &parsedValue); err == nil {
				return parsedValue, nil
			}

			return rendered, nil
		}
		return val, nil
	default:
		return val, nil
	}
}
