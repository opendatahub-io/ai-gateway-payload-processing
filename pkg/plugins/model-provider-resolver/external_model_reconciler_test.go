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
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
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

func newRef(providerName, targetModel, apiFormat, path string) inferencev1alpha1.ExternalProviderRef {
	return inferencev1alpha1.ExternalProviderRef{
		Ref:         inferencev1alpha1.NameReference{Name: providerName},
		TargetModel: targetModel,
		APIFormat:   apiFormat,
		Path:        path,
	}
}

func TestModelReconciler_HappyPath(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "gpt4"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("gpt4", "models", newRef("my-openai", "gpt-4o", "openai-chat", "/v1/chat/completions")),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-openai"},
		&providerInfo{
			provider: "openai", endpoint: "api.openai.com",
			auth:            auth.APIKey,
			secretName: "openai-key", secretNamespace: "models",
			config: map[string]string{},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	assert.Equal(t, "gpt4", info.modelName, "modelName defaults to metadata.name")
	require.Len(t, info.refs, 1)
	assert.Equal(t, "openai", info.refs[0].provider)
	assert.Equal(t, "gpt-4o", info.refs[0].targetModel)
	assert.Equal(t, apiformat.OpenAIChatCompletions, info.refs[0].apiFormat)
	assert.Equal(t, auth.APIKey, info.refs[0].auth)
	assert.Equal(t, "openai-key", info.refs[0].secretName)
	assert.Equal(t, 1, info.refs[0].weight)
}

func TestModelReconciler_ModelNameOverride(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "gpt4"}
	model := newTestModel("gpt4", "models", newRef("my-openai", "gpt-4o", "openai-chat", "/v1/chat/completions"))
	model.Spec.ModelName = "gpt-4o"

	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: model,
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-openai"},
		&providerInfo{
			provider: "openai", endpoint: "api.openai.com",
			secretName: "openai-key", secretNamespace: "models",
			config: map[string]string{},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getModelByName("gpt-4o")
	require.True(t, found)
	assert.Equal(t, "gpt-4o", info.modelName, "spec.modelName should override metadata.name")

	// Should NOT be findable by the CRD name
	_, foundByOldName := store.getModelByName(key.Name)
	assert.False(t, foundByOldName, "should not be findable by CRD name when modelName is set")
}

func TestModelReconciler_DeletedCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "deleted"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{}}

	store := newInfoStore()
	store.addOrUpdateModel(key.Name, &externalModelInfo{modelName: key.Name, refs: []*resolvedProviderRef{
		{provider: "openai", targetModel: "gpt-4o", weight: 1},
	}})

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.getModelByName(key.Name)
	assert.False(t, found, "store entry should be removed on delete")
}

func TestModelReconciler_ProviderNotAvailable(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "orphan"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("orphan", "models", newRef("missing-provider", "gpt-4o", "openai-chat", "/v1/chat/completions")),
	}}

	store := newInfoStore()
	r := &externalModelReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, providerRequeueDelay, result.RequeueAfter)

	_, found := store.getModelByName(key.Name)
	assert.False(t, found)
}

func TestModelReconciler_MultiRefAllResolved(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "multi"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("multi", "models",
			newRef("openai-provider", "gpt-4o", "openai-chat", "/v1/chat/completions"),
			newRef("azure-provider", "gpt-4o", "openai-chat", "/v1/chat/completions"),
		),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "openai-provider"},
		&providerInfo{provider: "openai", endpoint: "api.openai.com",
			secretName: "openai-key", secretNamespace: "models", config: map[string]string{}},
	)
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "azure-provider"},
		&providerInfo{provider: "azure-openai", endpoint: "my.openai.azure.com",
			secretName: "azure-key", secretNamespace: "models", config: map[string]string{}},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	require.Len(t, info.refs, 2, "both refs should be resolved")
	assert.Equal(t, "openai", info.refs[0].provider)
	assert.Equal(t, "azure-openai", info.refs[1].provider)
}

func TestModelReconciler_MultiRefPartialAvailability(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "partial"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("partial", "models",
			newRef("unavailable-provider", "gpt-4o", "openai-chat", "/v1/chat/completions"),
			newRef("available-provider", "claude-sonnet", "messages", "/v1/messages"),
		),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "available-provider"},
		&providerInfo{provider: "anthropic", endpoint: "api.anthropic.com",
			secretName: "anthropic-key", secretNamespace: "models", config: map[string]string{}},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	require.Len(t, info.refs, 1, "only the available ref should be stored")
	assert.Equal(t, "anthropic", info.refs[0].provider)
}

func TestModelReconciler_AuthOverride(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "auth-override"}
	ref := newRef("my-openai", "gpt-4o", "openai-chat", "/v1/chat/completions")
	ref.Auth = &inferencev1alpha1.AuthConfig{
		Type:      "apikey",
		SecretRef: inferencev1alpha1.NameReference{Name: "model-specific-key"},
	}

	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("auth-override", "models", ref),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-openai"},
		&providerInfo{provider: "openai", endpoint: "api.openai.com",
			auth: auth.SigV4, secretName: "provider-key", secretNamespace: "models", config: map[string]string{}},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	assert.Equal(t, auth.APIKey, info.refs[0].auth, "model-level auth overrides provider-level auth")
	assert.Equal(t, "model-specific-key", info.refs[0].secretName)
	assert.Equal(t, "models", info.refs[0].secretNamespace)
}

func TestModelReconciler_WeightFromCRD(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "weighted"}
	ref1 := newRef("openai-provider", "gpt-4o", "openai-chat", "/v1/chat/completions")
	ref1.Weight = ptr.To(80)
	ref2 := newRef("azure-provider", "gpt-4o", "openai-chat", "/v1/chat/completions")
	ref2.Weight = ptr.To(20)

	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("weighted", "models", ref1, ref2),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "openai-provider"},
		&providerInfo{provider: "openai", endpoint: "api.openai.com",
			secretName: "k1", secretNamespace: "models", config: map[string]string{}},
	)
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "azure-provider"},
		&providerInfo{provider: "azure-openai", endpoint: "my.openai.azure.com",
			secretName: "k2", secretNamespace: "models", config: map[string]string{}},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	require.Len(t, info.refs, 2)
	assert.Equal(t, 80, info.refs[0].weight)
	assert.Equal(t, 20, info.refs[1].weight)
}

func TestModelReconciler_WeightDefaultsToOne(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "no-weight"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("no-weight", "models", newRef("my-openai", "gpt-4o", "openai-chat", "/v1/chat/completions")),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-openai"},
		&providerInfo{provider: "openai", endpoint: "api.openai.com",
			secretName: "k", secretNamespace: "models", config: map[string]string{}},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	assert.Equal(t, 1, info.refs[0].weight, "weight should default to 1")
}

func TestModelReconciler_ConfigMerge(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "config-merge"}
	ref := newRef("my-vertex", "gemini-pro", "openai-chat", "/v1/chat/completions")
	ref.Config = map[string]string{"endpoint": "custom-endpoint"}

	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("config-merge", "models", ref),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "my-vertex"},
		&providerInfo{provider: "vertex-openai", endpoint: "us-central1-aiplatform.googleapis.com",
			secretName: "vertex-key", secretNamespace: "models",
			config: map[string]string{"project": "my-project", "location": "us-central1"}},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	assert.Equal(t, "my-project", info.refs[0].config["project"])
	assert.Equal(t, "us-central1", info.refs[0].config["location"])
	assert.Equal(t, "custom-endpoint", info.refs[0].config["endpoint"])
}

func TestModelReconciler_PathStoredFromRef(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "remote-llama"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("remote-llama", "models", newRef("cluster-b", "llama-4-scout", "openai-chat", "/maas-default-gateway/v1/chat/completions")),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "cluster-b"},
		&providerInfo{
			provider: "openai", endpoint: "maas.cluster-b.example.com",
			auth:       auth.APIKey,
			secretName: "cluster-b-key", secretNamespace: "models",
			config: map[string]string{},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	assert.Equal(t, "/maas-default-gateway/v1/chat/completions", info.refs[0].path,
		"resolved path should come from the model ref's required path field")
}

func TestModelReconciler_PathPlaceholderResolution(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "vertex-model"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("vertex-model", "models", newRef("gcp-vertex", "gemini-pro", "openai-chat", "/v1/projects/{project}/locations/{location}/publishers/google/models/gemini-pro:generateContent")),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "gcp-vertex"},
		&providerInfo{
			provider: "vertex", endpoint: "us-central1-aiplatform.googleapis.com",
			auth:       auth.APIKey,
			secretName: "vertex-key", secretNamespace: "models",
			config: map[string]string{"project": "my-project", "location": "us-central1"},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getModelByName(key.Name)
	require.True(t, found)
	assert.Equal(t, "/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-pro:generateContent",
		info.refs[0].path,
		"all placeholders should be resolved from config")
}

func TestModelReconciler_UnresolvedPlaceholderSkipsRef(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "bad-path"}
	reader := &mockModelReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalModel{
		key: newTestModel("bad-path", "models", newRef("gcp-vertex", "gemini-pro", "openai-chat", "/v1/projects/{project}/locations/{location}/endpoints/{endpoint}/chat/completions")),
	}}

	store := newInfoStore()
	store.addOrUpdateProvider(
		types.NamespacedName{Namespace: "models", Name: "gcp-vertex"},
		&providerInfo{
			provider: "vertex", endpoint: "us-central1-aiplatform.googleapis.com",
			auth:       auth.APIKey,
			secretName: "vertex-key", secretNamespace: "models",
			config: map[string]string{"project": "my-project"},
		},
	)

	r := &externalModelReconciler{Reader: reader, store: store}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, providerRequeueDelay, result.RequeueAfter, "should requeue when all refs fail validation")

	_, found := store.getModelByName(key.Name)
	assert.False(t, found, "model should not be stored when path has unresolved placeholders")
}

func TestMergeConfig(t *testing.T) {
	tests := []struct {
		name     string
		provider map[string]string
		model    map[string]string
		expected map[string]string
	}{
		{"nil both", nil, nil, map[string]string{}},
		{"provider only", map[string]string{"a": "1"}, nil, map[string]string{"a": "1"}},
		{"model overrides", map[string]string{"a": "1", "b": "2"}, map[string]string{"b": "override"}, map[string]string{"a": "1", "b": "override"}},
		{"model adds keys", map[string]string{"a": "1"}, map[string]string{"b": "2"}, map[string]string{"a": "1", "b": "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeConfig(tt.provider, tt.model)
			assert.Equal(t, tt.expected, result)
			if tt.provider != nil {
				result["mutated"] = "yes"
				_, leaked := tt.provider["mutated"]
				assert.False(t, leaked, "mergeConfig must return a copy")
			}
		})
	}
}
