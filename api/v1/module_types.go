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

type ModuleSourceGithubSpec struct{}

type ModuleSourceConfigMapRef struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// +optional
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key,omitempty"`
}

type ModuleSourceExistingHelmReleaseRef struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// ChartSource defines the Helm chart source for managing this release.
	// Required for installation and future operations (upgrades, reconfigurations).
	// +required
	ChartSource ModuleSourceChartRef `json:"chartSource"`

	// Values allows overriding specific values from the existing release.
	// These values will be merged with the existing release's values.
	// +optional
	Values *runtime.RawExtension `json:"values,omitempty"`
}

// ModuleSourceChartRef defines a reference to a Helm chart source
type ModuleSourceChartRef struct {
	// +optional
	ConfigMap *ModuleSourceConfigMapRef `json:"configMap,omitempty"`

	// Repository-based chart (e.g., from Helm repository)
	// +optional
	Repository *ModuleSourceChartRepository `json:"repository,omitempty"`

	// Git repository containing Helm chart
	// +optional
	Git *ModuleSourceChartGit `json:"git,omitempty"`
}

// ModuleSourceChartRepository defines a chart from a Helm repository
type ModuleSourceChartRepository struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// +required
	// +kubebuilder:validation:MinLength=1
	Chart string `json:"chart"`

	// +optional
	Version *string `json:"version,omitempty"`
}

// ModuleSourceChartGit defines a chart from a Git repository
type ModuleSourceChartGit struct {
	// Repository URL (https or ssh)
	// +required
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// Path to the chart directory containing Chart.yaml
	// +required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Git revision (branch, tag)
	// +kubebuilder:default=main
	Revision string `json:"revision"`

	// Authentication credentials for private repositories
	// +optional
	Auth *ModuleSourceChartGitAuth `json:"auth,omitempty"`
}

// ModuleSourceChartGitAuth defines authentication for Git repositories
type ModuleSourceChartGitAuth struct {
	// Reference to a Secret containing Git credentials
	// username and token fields
	// +optional
	HTTPSSecretRef *ModuleSourceChartGitAuthSecretRef `json:"httpsSecretRef"`

	// SShSecretRef ModuleSourceConfigMapRef `json:"sshSecretRef"` // TODO
}

type ModuleSourceChartGitAuthSecretRef struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:default=default
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
}

type ModuleSource struct {
	// +optional
	Raw *runtime.RawExtension `json:"raw,omitempty"`

	// +optional
	ConfigMap *ModuleSourceConfigMapRef `json:"configMap,omitempty"`

	// +optional
	HttpURL *string `json:"httpURL,omitempty"`

	// +optional
	Github *ModuleSourceGithubSpec `json:"github,omitempty"`

	// +optional
	ExistingHelmRelease *ModuleSourceExistingHelmReleaseRef `json:"existingHelmRelease,omitempty"`
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

// ModuleSpec defines the desired state of Module
type ModuleSpec struct {
	// +required
	Source ModuleSource `json:"source"`

	// +required
	Workspace ModuleWorkspaceReference `json:"workspace"`

	// +optional
	Config *runtime.RawExtension `json:"config,omitempty"`

	// +kubebuilder:default=false
	Hibernated bool `json:"hibernated"`

	// CreateNamespace specifies whether to create the target namespace if it doesn't exist.
	// Similar to ArgoCD's syncOptions.CreateNamespace.
	// +optional
	// +kubebuilder:default=false
	CreateNamespace bool `json:"createNamespace"`
}

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

	// Source indicates how this module is managed (e.g., "Adopted/Helm", "Managed/Git")
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
