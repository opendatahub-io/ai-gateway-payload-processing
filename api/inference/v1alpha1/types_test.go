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
