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

type mockModelReader struct {
	objects map[types.NamespacedName]*inferencev1alpha1.ExternalModel
}

func (m *mockModelReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	stored, ok := m.objects[key]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "inference.opendatahub.io", Resource: "externalmodels"}, key.Name)
	}
	*obj.(*inferencev1alpha1.ExternalModel) = *stored.DeepCopy()
	return nil
}

func (m *mockModelReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func newTestModel(name, ns string, refs ...inferencev1alpha1.ExternalProviderRef) *inferencev1alpha1.ExternalModel {
	return &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       inferencev1alpha1.ExternalModelSpec{ExternalProviderRefs: refs},
	}
}

func newRef(providerName, targetModel, apiFormat string) inferencev1alpha1.ExternalProviderRef {
	return inferencev1alpha1.ExternalProviderRef{
		Ref:         inferencev1alpha1.NameReference{Name: providerName},
		TargetModel: targetModel,
		APIFormat:   apiFormat,
	}
}

func TestModelReconciler_HappyPath(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "gpt4"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("gpt4", "models", newRef("my-openai", "gpt-4o", "openai-chat")),
	}}

	store := newInfoStore()
	providerKey := types.NamespacedName{Namespace: "models", Name: "my-openai"}
	store.addOrUpdateProvider(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "openai-key", secretNamespace: "models",
		config: map[string]string{},
	})

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModel(key)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "gpt-4o", info.targetModel)
	assert.Equal(t, "openai-chat", info.apiFormat)
	assert.Equal(t, "openai-key", info.secretName)
	assert.Equal(t, "models", info.secretNamespace)
}

func TestModelReconciler_DeletedCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "deleted"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{}}

	store := newInfoStore()
	store.addOrUpdateModel(key, &externalModelInfo{provider: "openai", targetModel: "gpt-4o"})

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.getModel(key)
	assert.False(t, found, "store entry should be removed on delete")
}

func TestModelReconciler_ProviderNotAvailable(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "orphan"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("orphan", "models", newRef("missing-provider", "gpt-4o", "openai-chat")),
	}}

	store := newInfoStore()
	r := &externalModelReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, providerRequeueDelay, result.RequeueAfter)

	_, found := store.getModel(key)
	assert.False(t, found)
}

func TestModelReconciler_MultiRefFallback(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "multi"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("multi", "models",
			newRef("unavailable-provider", "gpt-4o", "openai-chat"),
			newRef("available-provider", "claude-sonnet", "messages"),
		),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "available-provider"},
		&providerInfo{
			provider: "anthropic", endpoint: "api.anthropic.com",
			secretName: "anthropic-key", secretNamespace: "models",
			config: map[string]string{},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModel(key)
	require.True(t, found)
	assert.Equal(t, "anthropic", info.provider)
	assert.Equal(t, "claude-sonnet", info.targetModel)
	assert.Equal(t, "messages", info.apiFormat)
	assert.Equal(t, "anthropic-key", info.secretName)
}

func TestModelReconciler_AuthOverride(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "auth-override"}
	modelAuth := &inferencev1alpha1.AuthConfig{
		Type:      "simple",
		SecretRef: inferencev1alpha1.NameReference{Name: "model-specific-key"},
	}
	ref := newRef("my-openai", "gpt-4o", "openai-chat")
	ref.Auth = modelAuth

	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("auth-override", "models", ref),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-openai"},
		&providerInfo{
			provider: "openai", endpoint: "api.openai.com",
			secretName: "provider-key", secretNamespace: "models",
			config: map[string]string{},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModel(key)
	require.True(t, found)
	assert.Equal(t, "model-specific-key", info.secretName, "model auth should override provider auth")
	assert.Equal(t, "models", info.secretNamespace)
}

func TestModelReconciler_ConfigMerge(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "config-merge"}
	ref := newRef("my-vertex", "gemini-pro", "openai-chat")
	ref.Config = map[string]string{"endpoint": "custom-endpoint"}

	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("config-merge", "models", ref),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-vertex"},
		&providerInfo{
			provider: "vertex-openai", endpoint: "us-central1-aiplatform.googleapis.com",
			secretName: "vertex-key", secretNamespace: "models",
			config: map[string]string{"project": "my-project", "location": "us-central1"},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModel(key)
	require.True(t, found)
	assert.Equal(t, "my-project", info.config["project"], "provider config preserved")
	assert.Equal(t, "us-central1", info.config["location"], "provider config preserved")
	assert.Equal(t, "custom-endpoint", info.config["endpoint"], "model config overrides")
}

func TestMergeConfig(t *testing.T) {
	tests := []struct {
		name     string
		provider map[string]string
		model    map[string]string
		expected map[string]string
	}{
		{
			name:     "nil provider and model",
			provider: nil,
			model:    nil,
			expected: map[string]string{},
		},
		{
			name:     "provider only",
			provider: map[string]string{"a": "1"},
			model:    nil,
			expected: map[string]string{"a": "1"},
		},
		{
			name:     "model overrides provider",
			provider: map[string]string{"a": "1", "b": "2"},
			model:    map[string]string{"b": "override"},
			expected: map[string]string{"a": "1", "b": "override"},
		},
		{
			name:     "model adds new keys",
			provider: map[string]string{"a": "1"},
			model:    map[string]string{"b": "2"},
			expected: map[string]string{"a": "1", "b": "2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeConfig(tt.provider, tt.model)
			assert.Equal(t, tt.expected, result)

			// Verify result is a new map, not aliased
			if tt.provider != nil {
				result["mutated"] = "yes"
				_, leaked := tt.provider["mutated"]
				assert.False(t, leaked, "mergeConfig must return a copy, not alias the provider map")
			}
		})
	}
}
