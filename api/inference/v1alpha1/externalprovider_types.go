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
// +kubebuilder:printcolumn:name="Provider",type="string",JSONPath=".spec.provider"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.endpoint"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ExternalProvider defines a connection to an external LLM provider (endpoint + credentials).
// Multiple ExternalModel resources can reference the same ExternalProvider.
type ExternalProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalProviderSpec   `json:"spec,omitempty"`
	Status ExternalProviderStatus `json:"status,omitempty"`
}

// ExternalProviderSpec defines the desired state of ExternalProvider.
type ExternalProviderSpec struct {
	// Provider identifies the API type for this provider.
	// e.g. "openai", "anthropic", "azure-openai", "aws-bedrock", "vertex".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Provider string `json:"provider"`

	// Endpoint is the FQDN of the external provider (no scheme or path).
	// e.g. "api.openai.com", "bedrock.amazonaws.com".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)+$`
	Endpoint string `json:"endpoint"`

	// Auth configures how to authenticate with the provider.
	// +kubebuilder:validation:Required
	Auth AuthConfig `json:"auth"`

	// Config holds provider-specific configuration as key-value pairs.
	// e.g., Vertex AI: {"project": "my-project", "location": "us-central1"}.
	// +optional
	Config map[string]string `json:"config,omitempty"`
}

// AuthConfig defines the authentication method for an ExternalProvider.
type AuthConfig struct {
	// SecretRef references a Kubernetes Secret containing the provider API key.
	// The Secret must be in the same namespace as the ExternalProvider
	// and must contain a data key "api-key" with the credential value.
	// +kubebuilder:validation:Required
	SecretRef NameReference `json:"secretRef"`
}

// ExternalProviderStatus defines the observed state of ExternalProvider.
type ExternalProviderStatus struct {
	// Phase represents the current reconciliation phase.
	// Ready: all networking resources created and Secret validated.
	// Failed: reconciliation error (e.g., missing Secret, Istio resource creation failed).
	// This reflects controller reconciliation state, not runtime request health.
	// +kubebuilder:validation:Enum=Pending;Ready;Failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the provider's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// ExternalProviderList contains a list of ExternalProvider.
type ExternalProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalProvider{}, &ExternalProviderList{})
}
