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

type ModuleSourceExistingHelmReleaseRef struct {
	// +required
	Name string `json:"name"`

	// +optional
	Namespace string `json:"namespace,omitempty"`
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

type ModuleSourceConfigMapRef struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type ModuleWorkspaceReference struct {
	// +required
	Name string `json:"name"`

	// +optional
	// +kubebuilder:default=default
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

	// +optional
	// +kubebuilder:default=false
	Hibernated *bool `json:"hibernated,omitempty"`
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

	// +optional
	LastActivity *metav1.Time `json:"lastActivity,omitempty"`

	// +optional
	Message *string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mo
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Phase,type=string
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
