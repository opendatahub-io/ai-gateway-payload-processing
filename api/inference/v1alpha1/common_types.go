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

// AuthConfig defines the authentication method for an ExternalProvider.
type AuthConfig struct {
	// Type identifies the auth type for this provider.
	// e.g. "apikey" (header based), "sigv4", etc.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=apikey;sigv4;oauth2
	Type string `json:"type"`

	// SecretRef references a Kubernetes Secret containing the provider API key.
	// The Secret must be in the same namespace as the ExternalProvider
	// and must contain a data key "api-key" with the credential value.
	// +kubebuilder:validation:Required
	SecretRef NameReference `json:"secretRef"`
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
