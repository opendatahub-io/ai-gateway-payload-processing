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
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ExternalModel defines a client-facing model that maps to one or more
// external providers. The model name clients use in requests is determined
// by spec.modelName (if set) or metadata.name (default).
type ExternalModel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalModelSpec   `json:"spec,omitempty"`
	Status ExternalModelStatus `json:"status,omitempty"`
}

// ExternalModelSpec defines the desired state of ExternalModel.
type ExternalModelSpec struct {
	// ModelName is the client-facing model name used in inference request bodies.
	// Clients send this value in the "model" field of chat completion requests.
	// Defaults to metadata.name if not set. Use this field when the desired
	// model name contains characters not allowed in Kubernetes resource names
	// (dots, colons, slashes, uppercase).
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ModelName string `json:"modelName,omitempty"`

	// ExternalProviderRefs maps this model to one or more external providers.
	// Each entry specifies the provider specific details.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	ExternalProviderRefs []ExternalProviderRef `json:"externalProviderRefs"`

	// GatewayRefs explicitly selects the Praxis gateways that serve this model.
	// Omission retains tenant-local resolution; it does not select a global gateway.
	// Names are distinct across namespaces. An explicit empty list is invalid.
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	GatewayRefs []NamespacedObjectReference `json:"gatewayRefs,omitempty"`
}

// ExternalProviderRef binds this model to a specific provider with translation config.
type ExternalProviderRef struct {
	// Ref identifies the ExternalProvider CR. An omitted namespace means the
	// ExternalModel namespace.
	// +kubebuilder:validation:Required
	Ref ExternalProviderReference `json:"ref"`

	// TargetModel is the provider-specific model identifier.
	// e.g. "gpt-4o", "anthropic.claude-3-opus", "claude-sonnet-4-5-20241022".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	TargetModel string `json:"targetModel"`

	// APIFormat determines how requests/responses are translated for this provider.
	// Supported values:
	//   - "openai-chat": OpenAI Chat Completions API (/v1/chat/completions)
	//   - "messages": Anthropic Messages API (/v1/messages)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	APIFormat string `json:"apiFormat"`

	// Path sets the outgoing request `:path` pseudo-header for this model-provider binding.
	// Must be a valid absolute path (e.g. "/v1/chat/completions" or
	// "/maas-default-gateway/v1/chat/completions").
	// Supports {key} placeholders resolved from the merged Config map.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^/.*`
	Path string `json:"path"`

	// Config holds model-specific configuration as key-value pairs.
	// Overrides the ExternalProvider config for this model-provider binding.
	// +optional
	Config map[string]string `json:"config,omitempty"`

	// Auth overrides the ExternalProvider authentication for this model-provider binding.
	// Its Secret is in the ExternalModel namespace. If not set, the
	// ExternalProvider auth and its provider-local Secret are used.
	// +optional
	Auth *AuthConfig `json:"auth,omitempty"`

	// Weight determines the relative traffic proportion for this provider binding.
	// Higher weight means more traffic. Used for weighted random selection across
	// multiple provider refs. A weight of 0 disables the ref (no traffic routed
	// to it). Defaults to 1 if not set.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	Weight *int `json:"weight,omitempty"`
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

	// HTTPRouteName is the name of the HTTPRoute created by the controller
	// for this ExternalModel. Consumers (e.g., maas-controller) can read this
	// to attach policies without assuming naming conventions.
	// Retained for legacy models and a single gateway attachment whose route is
	// in the model namespace. Empty for multiple attachments or another route namespace.
	// +optional
	HTTPRouteName string `json:"httpRouteName,omitempty"`

	// ObservedGeneration is the latest spec generation processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// OverlayDigest is the content digest distributed to Praxis. It attests to
	// distribution, not to request-time serving.
	// +optional
	OverlayDigest string `json:"overlayDigest,omitempty"`

	// OverlayGeneration is the monotonic source generation distributed to Praxis.
	// +optional
	OverlayGeneration uint64 `json:"overlayGeneration,omitempty"`

	// Conditions represent the latest available observations of the model's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Gateways reports reconciliation and distribution readiness per gateway.
	// Aggregate Ready requires all requested attachments; an independently ready
	// attachment remains usable during partial failure. Readiness does not attest
	// to successful inference requests.
	// +optional
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	Gateways []ExternalModelGatewayStatus `json:"gateways,omitempty"`
}

// ExternalModelGatewayStatus is the result for one gateway attachment.
type ExternalModelGatewayStatus struct {
	// Name and Namespace identify the Gateway, not the route.
	NamespacedObjectReference `json:",inline"`

	// HTTPRouteRef identifies the HTTPRoute for this attachment, when available.
	// +optional
	HTTPRouteRef *NamespacedObjectReference `json:"httpRouteRef,omitempty"`

	// Conditions describe this attachment. Each condition's observedGeneration
	// identifies the ExternalModel generation it reflects. Missing, stale or
	// failed status must not fall back to another gateway's result.
	// +optional
	// +listType=map
	// +listMapKey=type
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
