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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

type mockProviderReader struct {
	objects map[types.NamespacedName]*inferencev1alpha1.ExternalProvider
}

func (m *mockProviderReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	stored, ok := m.objects[key]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "inference.opendatahub.io", Resource: "externalproviders"}, key.Name)
	}
	*obj.(*inferencev1alpha1.ExternalProvider) = *stored.DeepCopy()
	return nil
}

func (m *mockProviderReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func TestProviderReconciler_ValidCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{
		key: {
			ObjectMeta: metav1.ObjectMeta{Name: "my-openai", Namespace: "models"},
			Spec: inferencev1alpha1.ExternalProviderSpec{
				Provider: "openai",
				Endpoint: "api.openai.com",
				Auth:     inferencev1alpha1.AuthConfig{SecretRef: inferencev1alpha1.NameReference{Name: "openai-key"}},
			},
		},
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
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{}}

	store := newProviderInfoStore()
	store.addOrUpdate(key, &providerInfo{provider: "openai", endpoint: "api.openai.com"})

	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.get(key)
	assert.False(t, found, "store entry should be removed on delete")
}

func TestProviderReconciler_WithConfig(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-vertex"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{
		key: {
			ObjectMeta: metav1.ObjectMeta{Name: "my-vertex", Namespace: "models"},
			Spec: inferencev1alpha1.ExternalProviderSpec{
				Provider: "vertex-openai",
				Endpoint: "us-central1-aiplatform.googleapis.com",
				Auth:     inferencev1alpha1.AuthConfig{SecretRef: inferencev1alpha1.NameReference{Name: "vertex-key"}},
				Config:   map[string]string{"project": "my-project", "location": "us-central1"},
			},
		},
	}}
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
