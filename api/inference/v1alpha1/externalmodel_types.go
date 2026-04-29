/*
Copyright 2026 The opendatahub.io Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TargetModel",type="string",JSONPath=".spec.externalProviderRefs[0].targetModel"
// +kubebuilder:printcolumn:name="Provider",type="string",JSONPath=".spec.externalProviderRefs[0].ref.name"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ExternalModel defines a client-facing model name that maps to one or more
// external providers. metadata.name is the model name clients use in requests.
type ExternalModel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalModelSpec   `json:"spec,omitempty"`
	Status ExternalModelStatus `json:"status,omitempty"`
}

// ExternalModelSpec defines the desired state of ExternalModel.
type ExternalModelSpec struct {
	// ExternalProviderRefs maps this model to one or more external providers.
	// Each entry specifies the provider, provider-side model name, and API format.
	//
	// Phase 1: limited to a single provider ref. MaxItems will be increased
	// when traffic splitting is implemented in Phase 2.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	ExternalProviderRefs []ExternalProviderRef `json:"externalProviderRefs"`
}

// ExternalProviderRef binds this model to a specific provider with translation config.
type ExternalProviderRef struct {
	// Ref identifies the ExternalProvider CR (must be in the same namespace).
	// +kubebuilder:validation:Required
	Ref NameReference `json:"ref"`

	// TargetModel is the provider-specific model identifier.
	// e.g. "gpt-4o", "anthropic.claude-3-opus", "claude-sonnet-4-5-20241022".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	TargetModel string `json:"targetModel"`

	// APIFormat determines how requests/responses are translated for this provider.
	// e.g. "openai", "anthropic", "bedrock-openai", "azure-openai", "vertex-openai".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	APIFormat string `json:"apiFormat"`
}

// NameReference is a reference to a Kubernetes resource by name.
// The referenced resource must be in the same namespace.
type NameReference struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`
	Name string `json:"name"`
}

// ExternalModelStatus defines the observed state of ExternalModel.
type ExternalModelStatus struct {
	// Phase represents the current reconciliation phase.
	// Ready: all networking resources created successfully.
	// Failed: reconciliation error (e.g., missing ExternalProvider, missing Secret).
	// This reflects controller reconciliation state, not runtime request health.
	// +kubebuilder:validation:Enum=Pending;Ready;Failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the model's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// ExternalModelList contains a list of ExternalModel.
type ExternalModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalModel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalModel{}, &ExternalModelList{})
}
