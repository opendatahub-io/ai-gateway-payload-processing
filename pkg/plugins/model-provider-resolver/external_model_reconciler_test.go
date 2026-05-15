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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

func newModelCR(name, namespace, providerRefName, targetModel string) *inferencev1alpha1.ExternalModel {
	return &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: inferencev1alpha1.ExternalModelSpec{
			ExternalProviderRefs: []inferencev1alpha1.ExternalProviderRef{
				{
					Ref:         inferencev1alpha1.NameReference{Name: providerRefName},
					TargetModel: targetModel,
					APIFormat:   "chat-completions",
				},
			},
		},
	}
}

func TestModelReconciler_ValidCR(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4"}
	providerKey := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{
		modelKey: newModelCR("gpt4", "models", "my-openai", "gpt-4o"),
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

	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{
		modelKey: newModelCR("gpt4", "models", "my-openai", "gpt-4o"),
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

func TestModelReconciler_DeletedCR(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "deleted"}

	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{}} // not found

	modelStore := newModelInfoStore()
	modelStore.addOrUpdateExternalModel(modelKey, &externalModelInfo{provider: "openai", targetModel: "gpt-4o"})

	provStore := newProviderInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := modelStore.getModelInfo(modelKey)
	assert.False(t, found, "should remove model from store on delete")
}

func TestModelReconciler_ProviderConfigPropagation(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "vertex-model"}
	providerKey := types.NamespacedName{Namespace: "models", Name: "my-vertex"}

	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{
		modelKey: newModelCR("vertex-model", "models", "my-vertex", "gemini-pro"),
	}}

	provStore := newProviderInfoStore()
	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "vertex-openai", endpoint: "us-central1-aiplatform.googleapis.com",
		secretName: "vertex-key", secretNamespace: "models",
		config: map[string]string{"project": "my-project", "location": "us-central1"},
	})

	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)

	info, found := modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "my-project", info.config["project"])
	assert.Equal(t, "us-central1", info.config["location"])
}

func TestModelReconciler_ProviderUpdate_Propagates(t *testing.T) {
	modelKey := types.NamespacedName{Namespace: "models", Name: "gpt4"}
	providerKey := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	reader := &typedMockReader{objects: map[types.NamespacedName]client.Object{
		modelKey: newModelCR("gpt4", "models", "my-openai", "gpt-4o"),
	}}

	provStore := newProviderInfoStore()
	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "old-key", secretNamespace: "models",
	})

	modelStore := newModelInfoStore()
	r := &externalModelReconciler{Reader: reader, modelStore: modelStore, providerStore: provStore}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)

	info, _ := modelStore.getModelInfo(modelKey)
	assert.Equal(t, "old-key", info.secretName)

	// Update provider credentials
	provStore.addOrUpdate(providerKey, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "new-key", secretNamespace: "models",
	})

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: modelKey})
	require.NoError(t, err)

	info, found := modelStore.getModelInfo(modelKey)
	require.True(t, found)
	assert.Equal(t, "new-key", info.secretName, "model store should reflect updated provider credentials")
}

// TestModelReconciler_NoProviderRefs is removed — CRD validation (MinItems=1)
// prevents ExternalModel CRs with empty externalProviderRefs from being created.
