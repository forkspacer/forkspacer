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
)

// +kubebuilder:validation:Enum=ready;hibernated;failed;terminating
type WorkspacePhase string

const (
	WorkspacePhaseReady       WorkspacePhase = "ready"
	WorkspacePhaseHibernated  WorkspacePhase = "hibernated"
	WorkspacePhaseFailed      WorkspacePhase = "failed"
	WorkspacePhaseTerminating WorkspacePhase = "terminating"
)

// +kubebuilder:validation:Enum=kubernetes
type WorkspaceType string

const (
	WorkspaceTypeKubernetes WorkspaceType = "kubernetes"
)

// +kubebuilder:validation:Enum=local;in-cluster;kubeconfig
type WorkspaceConnectionType string

const (
	WorkspaceConnectionTypeLocal      WorkspaceConnectionType = "local"
	WorkspaceConnectionTypeInCluster  WorkspaceConnectionType = "in-cluster"
	WorkspaceConnectionTypeKubeconfig WorkspaceConnectionType = "kubeconfig"
)

type WorkspaceConnectionSecretReference struct {
	Name string `json:"name"`

	// +optional
	// +kubebuilder:default=default
	Namespace string `json:"namespace"`
}

type WorkspaceConnection struct {
	// +kubebuilder:default=local
	Type WorkspaceConnectionType `json:"type"`

	// +optional
	SecretReference *WorkspaceConnectionSecretReference `json:"secretReference,omitempty"`
}

type WorkspaceAutoHibernation struct {
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// +kubebuilder:validation:MinLength=0
	Schedule string `json:"schedule"`

	// +kubebuilder:validation:MinLength=0
	// +optional
	WakeSchedule *string `json:"wakeSchedule,omitempty"`
}

type WorkspaceFromReference struct {
	Name string `json:"name"`

	// +kubebuilder:default=default
	Namespace string `json:"namespace"`
}

// WorkspaceSpec defines the desired state of Workspace
type WorkspaceSpec struct {
	// +optional
	// +kubebuilder:default=kubernetes
	Type WorkspaceType `json:"type"`

	// *optional
	From *WorkspaceFromReference `json:"from,omitempty"`

	// +optional
	// +kubebuilder:default=false
	Hibernated *bool `json:"hibernated,omitempty"`

	// +optional
	Connection *WorkspaceConnection `json:"connection,omitempty"`

	// +optional
	AutoHibernation *WorkspaceAutoHibernation `json:"autoHibernation,omitempty"`
}

// WorkspaceStatus defines the observed state of Workspace.
type WorkspaceStatus struct {
	// conditions represent the current state of the Workspace resource.
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

	Phase WorkspacePhase `json:"phase"`

	// +optional
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// +optional
	LastActivity *metav1.Time `json:"lastActivity,omitempty"`

	// +optional
	HibernatedAt *metav1.Time `json:"hibernatedAt,omitempty"`

	// +optional
	Message *string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ws
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Phase,type=string
// +kubebuilder:printcolumn:JSONPath=".status.ready",name=Ready,type=boolean
// +kubebuilder:printcolumn:JSONPath=".status.lastActivity",name=Last Activity,type=string,format=date-time
// +kubebuilder:printcolumn:JSONPath=".status.hibernatedAt",name=Hibernated At,type=string,format=date-time
// +kubebuilder:printcolumn:JSONPath=".status.message",name=Message,type=string

// Workspace is the Schema for the workspaces API
type Workspace struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Workspace
	// +required
	Spec WorkspaceSpec `json:"spec"`

	// +optional
	Status WorkspaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkspaceList contains a list of Workspace
type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workspace{}, &WorkspaceList{})
}
