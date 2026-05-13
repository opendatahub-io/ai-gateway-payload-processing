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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// mockReader implements client.Reader for unit testing reconcilers.
type mockReader struct {
	objects map[types.NamespacedName]*unstructured.Unstructured
}

func (m *mockReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	u, ok := m.objects[key]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "inference.opendatahub.io", Resource: "externalproviders"}, key.Name)
	}
	u.DeepCopyInto(obj.(*unstructured.Unstructured))
	return nil
}

func (m *mockReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func newProviderUnstructured(name, namespace, provider, endpoint, secretName string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalProviderGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.Object["spec"] = map[string]any{
		"provider": provider,
		"endpoint": endpoint,
		"auth": map[string]any{
			"secretRef": map[string]any{
				"name": secretName,
			},
		},
	}
	return obj
}

func TestProviderReconciler_ValidCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}
	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newProviderUnstructured("my-openai", "models", "openai", "api.openai.com", "openai-key"),
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
	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{}} // not found
	store := newProviderInfoStore()
	store.addOrUpdate(key, &providerInfo{provider: "openai"})

	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.get(key)
	assert.False(t, found, "store entry should be removed on delete")
}

func TestProviderReconciler_MissingProvider(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "bad"}
	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newProviderUnstructured("bad", "models", "", "api.openai.com", "key"),
	}}
	store := newProviderInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.get(key)
	assert.False(t, found, "should not store provider with empty provider field")
}

func TestProviderReconciler_MissingEndpoint(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "bad"}
	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newProviderUnstructured("bad", "models", "openai", "", "key"),
	}}
	store := newProviderInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.get(key)
	assert.False(t, found, "should not store provider with empty endpoint")
}

func TestProviderReconciler_MissingSecretName(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "bad"}
	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newProviderUnstructured("bad", "models", "openai", "api.openai.com", ""),
	}}
	store := newProviderInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.get(key)
	assert.False(t, found, "should not store provider with empty secretName")
}
