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
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	gvk := schema.GroupVersionKind{Group: "inference.opendatahub.io", Version: "v1alpha1", Kind: "ExternalProvider"}
	obj, err := scheme.New(gvk)
	require.NoError(t, err)
	assert.IsType(t, &ExternalProvider{}, obj)

	gvk.Kind = "ExternalModel"
	obj, err = scheme.New(gvk)
	require.NoError(t, err)
	assert.IsType(t, &ExternalModel{}, obj)
}

func TestExternalProviderDeepCopy(t *testing.T) {
	original := &ExternalProvider{
		Spec: ExternalProviderSpec{
			Provider: "openai",
			Endpoint: "api.openai.com",
			Auth:     AuthConfig{SecretRef: NameReference{Name: "key"}},
			Config:   map[string]string{"project": "my-project", "location": "us-central1"},
		},
	}

	copied := original.DeepCopy()

	assert.Equal(t, original.Spec, copied.Spec)

	// Verify deep copy — mutating the copy must not affect the original
	copied.Spec.Config["project"] = "other-project"
	assert.Equal(t, "my-project", original.Spec.Config["project"])
}

func TestExternalModelDeepCopy(t *testing.T) {
	original := &ExternalModel{
		Spec: ExternalModelSpec{
			ExternalProviderRefs: []ExternalProviderRef{
				{
					Ref:         NameReference{Name: "my-openai"},
					TargetModel: "gpt-4o",
					APIFormat:   "openai-chat",
				},
			},
		},
	}

	copied := original.DeepCopy()

	assert.Equal(t, original.Spec, copied.Spec)

	// Verify deep copy — mutating the copy must not affect the original
	copied.Spec.ExternalProviderRefs[0].TargetModel = "gpt-3.5"
	assert.Equal(t, "gpt-4o", original.Spec.ExternalProviderRefs[0].TargetModel)
}

// CRD schema validation (patterns) is enforced at admission time by the K8s API server,
// not in Go structs. These tests verify the regex patterns themselves are correct.

func TestNameReferencePattern(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

	valid := []string{"my-openai", "a", "openai-key-v2", "a1b2"}
	for _, name := range valid {
		assert.True(t, pattern.MatchString(name), "should accept %q", name)
	}

	invalid := []string{"My-OpenAI", "UPPERCASE", "-leading-dash", "trailing-", "has/slash", "has.dot", "has space"}
	for _, name := range invalid {
		assert.False(t, pattern.MatchString(name), "should reject %q", name)
	}
}

func TestEndpointPattern(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)+$`)

	valid := []string{"api.openai.com", "bedrock.us-east-1.amazonaws.com", "3-13-21-181.sslip.io", "a.b"}
	for _, ep := range valid {
		assert.True(t, pattern.MatchString(ep), "should accept %q", ep)
	}

	invalid := []string{"openai", "localhost", "-bad.com", "bad-.com", "has space.com", "has/slash.com"}
	for _, ep := range invalid {
		assert.False(t, pattern.MatchString(ep), "should reject %q", ep)
	}
}
