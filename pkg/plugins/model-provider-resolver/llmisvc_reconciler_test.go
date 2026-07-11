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

type mockLLMISvcReader struct {
	objects map[types.NamespacedName]*unstructured.Unstructured
}

func (m *mockLLMISvcReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	stored, ok := m.objects[key]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "serving.kserve.io", Resource: "llminferenceservices"}, key.Name)
	}
	target := obj.(*unstructured.Unstructured)
	stored.DeepCopyInto(target)
	return nil
}

func (m *mockLLMISvcReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func newTestLLMISvc(name, ns, modelName string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "serving.kserve.io/v1alpha1",
			"kind":       "LLMInferenceService",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"model": map[string]interface{}{
					"uri": "hf://placeholder",
				},
			},
		},
	}
	if modelName != "" {
		_ = unstructured.SetNestedField(obj.Object, modelName, "spec", "model", "name")
	}
	return obj
}

func TestLLMISvcReconciler_HappyPath(t *testing.T) {
	key := types.NamespacedName{Namespace: "llm", Name: "facebook-opt-125m-simulated"}
	reader := &mockLLMISvcReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newTestLLMISvc("facebook-opt-125m-simulated", "llm", "facebook/opt-125m"),
	}}

	store := newInfoStore()
	r := &llmisvcReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getLLMISvcByName("facebook/opt-125m")
	require.True(t, found)
	assert.Equal(t, "facebook/opt-125m", info.modelName)
	assert.Equal(t, "publishers/llm/models/facebook/opt-125m", info.publisherID)
	assert.Equal(t, key.String(), info.key)
}

func TestLLMISvcReconciler_FallbackToMetadataName(t *testing.T) {
	key := types.NamespacedName{Namespace: "llm", Name: "my-model"}
	reader := &mockLLMISvcReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newTestLLMISvc("my-model", "llm", ""),
	}}

	store := newInfoStore()
	r := &llmisvcReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getLLMISvcByName("my-model")
	require.True(t, found)
	assert.Equal(t, "publishers/llm/models/my-model", info.publisherID)
}

func TestLLMISvcReconciler_Deleted(t *testing.T) {
	key := types.NamespacedName{Namespace: "llm", Name: "deleted-model"}
	reader := &mockLLMISvcReader{objects: map[types.NamespacedName]*unstructured.Unstructured{}}

	store := newInfoStore()
	store.addOrUpdateLLMISvc("facebook/opt-125m", &llmisvcModelInfo{
		modelName:   "facebook/opt-125m",
		publisherID: "publishers/llm/models/facebook/opt-125m",
		key:         key.String(),
	})

	r := &llmisvcReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.getLLMISvcByName("facebook/opt-125m")
	assert.False(t, found, "store entry should be removed on delete")
}

func TestLLMISvcReconciler_UpdateModelName(t *testing.T) {
	key := types.NamespacedName{Namespace: "llm", Name: "facebook-opt-125m-simulated"}

	store := newInfoStore()
	store.addOrUpdateLLMISvc("old-model-name", &llmisvcModelInfo{
		modelName:   "old-model-name",
		publisherID: "publishers/llm/models/old-model-name",
		key:         key.String(),
	})

	reader := &mockLLMISvcReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key: newTestLLMISvc("facebook-opt-125m-simulated", "llm", "new-model-name"),
	}}

	r := &llmisvcReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	_, foundOld := store.getLLMISvcByName("old-model-name")
	assert.False(t, foundOld, "old model name entry should be cleaned up")

	info, foundNew := store.getLLMISvcByName("new-model-name")
	require.True(t, foundNew)
	assert.Equal(t, "publishers/llm/models/new-model-name", info.publisherID)
}

func TestLLMISvcReconciler_DifferentNamespaces(t *testing.T) {
	key1 := types.NamespacedName{Namespace: "ns1", Name: "model-a"}
	key2 := types.NamespacedName{Namespace: "ns2", Name: "model-b"}

	reader := &mockLLMISvcReader{objects: map[types.NamespacedName]*unstructured.Unstructured{
		key1: newTestLLMISvc("model-a", "ns1", "shared/model"),
		key2: newTestLLMISvc("model-b", "ns2", "other/model"),
	}}

	store := newInfoStore()
	r := &llmisvcReconciler{Reader: reader, store: store}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key1})
	require.NoError(t, err)
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key2})
	require.NoError(t, err)

	info1, found1 := store.getLLMISvcByName("shared/model")
	require.True(t, found1)
	assert.Equal(t, "publishers/ns1/models/shared/model", info1.publisherID)

	info2, found2 := store.getLLMISvcByName("other/model")
	require.True(t, found2)
	assert.Equal(t, "publishers/ns2/models/other/model", info2.publisherID)
}

func TestExtractModelName(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected string
	}{
		{
			name:     "spec.model.name set",
			obj:      newTestLLMISvc("k8s-name", "ns", "facebook/opt-125m"),
			expected: "facebook/opt-125m",
		},
		{
			name:     "spec.model.name empty",
			obj:      newTestLLMISvc("k8s-name", "ns", ""),
			expected: "k8s-name",
		},
		{
			name: "no spec.model at all",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"metadata": map[string]interface{}{"name": "fallback-name"},
				"spec":     map[string]interface{}{},
			}},
			expected: "fallback-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractModelName(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}
