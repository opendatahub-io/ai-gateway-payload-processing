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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func TestProviderStore_AddGetDelete(t *testing.T) {
	store := newInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	_, found := store.getProvider(key)
	assert.False(t, found)

	store.addOrUpdateProvider(key, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "key", secretNamespace: "models",
	})

	info, found := store.getProvider(key)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "api.openai.com", info.endpoint)

	store.deleteProvider(key)
	_, found = store.getProvider(key)
	assert.False(t, found)
}

func TestProviderStore_Update(t *testing.T) {
	store := newInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	store.addOrUpdateProvider(key, &providerInfo{provider: "openai", endpoint: "old.com"})
	store.addOrUpdateProvider(key, &providerInfo{provider: "openai", endpoint: "new.com"})

	info, _ := store.getProvider(key)
	assert.Equal(t, "new.com", info.endpoint)
}

func TestLLMISvcStore_AddGetDelete(t *testing.T) {
	store := newInfoStore()

	_, found := store.getLLMISvcByName("facebook/opt-125m")
	assert.False(t, found)

	store.addOrUpdateLLMISvc("facebook/opt-125m", &llmisvcModelInfo{
		modelName:   "facebook/opt-125m",
		publisherID: "publishers/llm/models/facebook/opt-125m",
		key:         "llm/facebook-opt-125m-simulated",
	})

	info, found := store.getLLMISvcByName("facebook/opt-125m")
	require.True(t, found)
	assert.Equal(t, "publishers/llm/models/facebook/opt-125m", info.publisherID)

	store.deleteLLMISvcByKey("llm/facebook-opt-125m-simulated")
	_, found = store.getLLMISvcByName("facebook/opt-125m")
	assert.False(t, found)
}

func TestLLMISvcStore_UpdateCleansOldEntry(t *testing.T) {
	store := newInfoStore()
	key := "llm/my-model"

	store.addOrUpdateLLMISvc("old-name", &llmisvcModelInfo{
		modelName: "old-name", publisherID: "publishers/llm/models/old-name", key: key,
	})
	store.addOrUpdateLLMISvc("new-name", &llmisvcModelInfo{
		modelName: "new-name", publisherID: "publishers/llm/models/new-name", key: key,
	})

	_, foundOld := store.getLLMISvcByName("old-name")
	assert.False(t, foundOld, "old model name should be removed")

	info, foundNew := store.getLLMISvcByName("new-name")
	require.True(t, foundNew)
	assert.Equal(t, "publishers/llm/models/new-name", info.publisherID)
}

func TestLLMISvcStore_DeleteNonexistent(t *testing.T) {
	store := newInfoStore()
	store.deleteLLMISvcByKey("nonexistent/key")
}

func TestProviderStore_WithConfig(t *testing.T) {
	store := newInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-vertex"}

	store.addOrUpdateProvider(key, &providerInfo{
		provider: "vertex-openai", endpoint: "aiplatform.googleapis.com",
		secretName: "key", secretNamespace: "models",
		config: map[string]string{"project": "my-project", "location": "us-central1"},
	})

	info, found := store.getProvider(key)
	require.True(t, found)
	assert.Equal(t, "my-project", info.config["project"])
}
