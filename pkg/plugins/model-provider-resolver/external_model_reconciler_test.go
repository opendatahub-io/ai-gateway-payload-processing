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
	"time"

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
				"apiFormat":   "openai",
				"weight":      float64(1),
			},
		},
	}
	return obj
}

func newMultiProviderModelUnstructured(name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalModelGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.Object["spec"] = map[string]any{
		"externalProviderRefs": []any{
			map[string]any{
				"ref":         map[string]any{"name": "openai-provider"},
				"targetModel": "gpt-4o",
				"apiFormat":   "openai",
				"weight":      float64(80),
			},
			map[string]any{
				"ref":         map[string]any{"name": "bedrock-provider"},
				"targetModel": "gpt-4o-bedrock",
				"apiFormat":   "bedrock-openai",
				"weight":      float64(20),
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
	assert.Equal(t, "gpt4", info.modelName)
	require.Len(t, info.refs, 1)
	assert.Equal(t, "openai", info.refs[0].provider)
	assert.Equal(t, "gpt-4o", info.refs[0].targetModel)
	assert.Equal(t, "openai-key", info.refs[0].secretName)
	assert.Equal(t, "openai", info.refs[0].apiFormat)
}

func TestModelReconciler_MultiProvider(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newMultiProviderModelUnstructured("gpt4", "models"),
	}}

	provStore := newProviderInfoStore()
	provStore.addOrUpdate(types.NamespacedName{Namespace: "models", Name: "openai-provider"}, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "openai-key", secretNamespace: "models",
	})
	provStore.addOrUpdate(types.NamespacedName{Namespace: "models", Name: "bedrock-provider"}, &providerInfo{
		provider: "bedrock-openai", endpoint: "bedrock.amazonaws.com",
		secretName: "aws-key", secretNamespace: "models",
	})

	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := modelStore.getModelInfo(modelKey)
	require.True(t, found)
	require.Len(t, info.refs, 2)
	assert.Equal(t, "openai-provider", info.refs[0].providerName)
	assert.Equal(t, int32(80), info.refs[0].weight)
	assert.Equal(t, "bedrock-provider", info.refs[1].providerName)
	assert.Equal(t, int32(20), info.refs[1].weight)
}

func TestModelReconciler_MissingProvider_Requeues(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newModelUnstructured("gpt4", "models", "my-openai", "gpt-4o"),
	}}

	provStore := newProviderInfoStore()
	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, result.RequeueAfter)

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found)
}

func TestModelReconciler_EmptyProviderRef_NoRequeue(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "bad"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		modelKey: newModelUnstructured("bad", "models", "", "gpt-4o"),
	}}

	provStore := newProviderInfoStore()
	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.Zero(t, result.RequeueAfter)

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found)
}

func TestModelReconciler_DeletedCR(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "deleted"}

	reader := &mockReader{objects: map[types.NamespacedName]*unstructured.Unstructured{}}

	modelStore := newModelInfoStore()
	modelStore.addOrUpdateExternalModel(modelKey, &externalModelInfo{
		modelName: "deleted",
		refs:      []providerRef{{provider: "openai", targetModel: "gpt-4o", weight: 1}},
	})

	provStore := newProviderInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found)
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

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "old-key", info.refs[0].secretName)

	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "new-key", secretNamespace: "models",
	})

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)

	info, found = modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "new-key", info.refs[0].secretName)
}
