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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func TestProviderStore_AddAndGet(t *testing.T) {
	store := newProviderInfoStore()
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}

	store.addOrUpdate(key, &providerInfo{
		provider: "openai", endpoint: "api.openai.com",
		secretName: "openai-key", secretNamespace: "models",
	})

	info, found := store.get(key)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "api.openai.com", info.endpoint)
	assert.Equal(t, "openai-key", info.secretName)
	assert.Equal(t, "models", info.secretNamespace)
}

func TestProviderStore_GetNotFound(t *testing.T) {
	store := newProviderInfoStore()
	_, found := store.get(types.NamespacedName{Namespace: "ns", Name: "nope"})
	assert.False(t, found)
}

func TestProviderStore_Delete(t *testing.T) {
	store := newProviderInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "prov"}
	store.addOrUpdate(key, &providerInfo{provider: "openai"})

	store.delete(key)

	_, found := store.get(key)
	assert.False(t, found)
}

func TestProviderStore_Update(t *testing.T) {
	store := newProviderInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "prov"}

	store.addOrUpdate(key, &providerInfo{provider: "openai", endpoint: "api.openai.com"})
	store.addOrUpdate(key, &providerInfo{provider: "openai", endpoint: "api-v2.openai.com"})

	info, found := store.get(key)
	require.True(t, found)
	assert.Equal(t, "api-v2.openai.com", info.endpoint)
}

func TestProviderStore_ConcurrentAccess(t *testing.T) {
	store := newProviderInfoStore()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := types.NamespacedName{Namespace: "ns", Name: fmt.Sprintf("prov-%d", n%10)}
			store.addOrUpdate(key, &providerInfo{provider: fmt.Sprintf("type-%d", n)})
			store.get(key)
			if n%3 == 0 {
				store.delete(key)
			}
		}(i)
	}

	wg.Wait()
}
