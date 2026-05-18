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
	store := newProviderInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	_, found := store.get(key)
	assert.False(t, found)

	store.addOrUpdate(key, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "key", secretNamespace: "models",
	})

	info, found := store.get(key)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "api.openai.com", info.endpoint)

	store.delete(key)
	_, found = store.get(key)
	assert.False(t, found)
}

func TestProviderStore_Update(t *testing.T) {
	store := newProviderInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	store.addOrUpdate(key, &providerInfo{provider: "openai", endpoint: "old.com"})
	store.addOrUpdate(key, &providerInfo{provider: "openai", endpoint: "new.com"})

	info, _ := store.get(key)
	assert.Equal(t, "new.com", info.endpoint)
}

func TestProviderStore_WithConfig(t *testing.T) {
	store := newProviderInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-vertex"}

	store.addOrUpdate(key, &providerInfo{
		provider: "vertex-openai", endpoint: "aiplatform.googleapis.com",
		secretName: "key", secretNamespace: "models",
		config: map[string]string{"project": "my-project", "location": "us-central1"},
	})

	info, found := store.get(key)
	require.True(t, found)
	assert.Equal(t, "my-project", info.config["project"])
}
