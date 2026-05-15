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

package model_provider_resolver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

// typedMockReader implements client.Reader for typed objects.
type typedMockReader struct {
	objects map[types.NamespacedName]client.Object
}

func (m *typedMockReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	stored, ok := m.objects[key]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "inference.opendatahub.io"}, key.Name)
	}
	stored.(runtime.Object).DeepCopyObject()
	// Copy into the target
	switch target := obj.(type) {
	case *inferencev1alpha1.ExternalProvider:
		src := stored.(*inferencev1alpha1.ExternalProvider)
		*target = *src.DeepCopy()
	case *inferencev1alpha1.ExternalModel:
		src := stored.(*inferencev1alpha1.ExternalModel)
		*target = *src.DeepCopy()
	}
	return nil
}

func (m *typedMockReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func newProviderCR(name, namespace, provider, endpoint, secretName string) *inferencev1alpha1.ExternalProvider {
	return &inferencev1alpha1.ExternalProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: inferencev1alpha1.ExternalProviderSpec{
			Provider: provider,
			Endpoint: endpoint,
			Auth:     inferencev1alpha1.AuthConfig{SecretRef: inferencev1alpha1.NameReference{Name: secretName}},
		},
	}
}

func TestProviderReconciler_ValidCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}
	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{
		key: newProviderCR("my-openai", "models", "openai", "api.openai.com", "openai-key"),
	}}
	store := newProviderInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.get(key)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "api.openai.com", info.endpoint)
	assert.Equal(t, "openai-key", info.secretName)
	assert.Equal(t, "models", info.secretNamespace)
}

func TestProviderReconciler_DeletedCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "deleted"}
	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{}} // not found

	store := newProviderInfoStore()
	store.addOrUpdate(key, &providerInfo{provider: "openai", endpoint: "api.openai.com"})

	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.get(key)
	assert.False(t, found, "store entry should be removed on delete")
}

// TestProviderReconciler_MissingProvider, TestProviderReconciler_MissingEndpoint,
// TestProviderReconciler_MissingSecretName removed — CRD validation (Required fields)
// prevents ExternalProvider CRs with empty provider/endpoint/secretRef from being created.

func TestProviderReconciler_WithConfig(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-vertex"}
	provider := newProviderCR("my-vertex", "models", "vertex-openai", "us-central1-aiplatform.googleapis.com", "vertex-key")
	provider.Spec.Config = map[string]string{"project": "my-project", "location": "us-central1"}

	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{key: provider}}
	store := newProviderInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.get(key)
	require.True(t, found)
	assert.Equal(t, "my-project", info.config["project"])
	assert.Equal(t, "us-central1", info.config["location"])
}
