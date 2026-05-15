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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func newModelUnstructured(name, namespace, providerRefName, targetModel string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalModelGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.Object["spec"] = map[string]any{
		"externalProviderRefs": []any{
			map[string]any{
				"ref":         map[string]any{"name": providerRefName},
				"targetModel": targetModel,
			},
		},
	}
	return obj
}

func TestModelReconciler_ValidCR(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4"}
	providerKey := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newModelUnstructured("gpt4", "models", "my-openai", "gpt-4o"),
	}}

	provStore := newProviderInfoStore()
	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "openai-key", secretNamespace: "models",
	})

	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "gpt-4o", info.targetModel)
	assert.Equal(t, "openai-key", info.secretName)
	assert.Equal(t, "models", info.secretNamespace)
}

func TestModelReconciler_MissingProvider_Requeues(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newModelUnstructured("gpt4", "models", "my-openai", "gpt-4o"),
	}}

	provStore := newProviderInfoStore() // empty — provider not yet reconciled
	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter, "should requeue when provider not available")

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found, "should not populate model store without provider")
}

func TestModelReconciler_EmptyProviderRef_Requeues(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "bad"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newModelUnstructured("bad", "models", "", "gpt-4o"),
	}}

	provStore := newProviderInfoStore()
	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	// Empty provider ref name → provider store lookup fails → requeue
	assert.NotZero(t, result.RequeueAfter, "should requeue when provider not found (empty ref name)")

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found)
}

func TestModelReconciler_DeletedCR(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "deleted"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{}} // not found

	modelStore := newModelInfoStore()
	modelStore.addOrUpdateExternalModel(modelKey, &externalModelInfo{provider: "openai", targetModel: "gpt-4o"})

	provStore := newProviderInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found, "model store entry should be removed on delete")
}

func TestModelReconciler_ProviderUpdatePropagates(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4-update"}
	providerKey := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newModelUnstructured("gpt4-update", "models", "my-openai", "gpt-4o"),
	}}

	provStore := newProviderInfoStore()
	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "old-key", secretNamespace: "models",
	})

	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	// First reconcile — model gets old-key
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "old-key", info.secretName)

	// Simulate provider update (credential rotation)
	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "new-key", secretNamespace: "models",
	})

	// Re-reconcile (triggered by cross-watch in production) — model picks up new-key
	result, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found = modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "new-key", info.secretName, "model store should reflect updated provider credentials")
}

// TestModelReconciler_NoProviderRefs is removed — CRD validation (MinItems=1)
// prevents ExternalModel CRs with empty externalProviderRefs from being created.
