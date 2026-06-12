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
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
)

func TestModelStore_AddAndGetExternalModel(t *testing.T) {
	store := newInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "external-model"}

	store.addOrUpdateModel(key, &externalModelInfo{refs: []*resolvedProviderRef{
		{provider: provider.Anthropic, weight: 1},
	}})

	info, found := store.getModel(key)
	assert.True(t, found)
	assert.NotNil(t, info)
	assert.Equal(t, provider.Anthropic, info.refs[0].provider)
}

func TestModelStore_GetModelInfo_NotFound(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: "ns", Name: "ext"},
		&externalModelInfo{refs: []*resolvedProviderRef{{provider: provider.OpenAI, weight: 1}}},
	)

	_, found := store.getModel(types.NamespacedName{Namespace: "ns", Name: "other"})
	assert.False(t, found)
}

func TestModelStore_DeleteExternalModel(t *testing.T) {
	store := newInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "ext"}
	store.addOrUpdateModel(key, &externalModelInfo{refs: []*resolvedProviderRef{
		{provider: provider.OpenAI, weight: 1},
	}})

	_, foundBefore := store.getModel(key)
	assert.True(t, foundBefore)

	store.deleteModel(key)
	_, foundAfter := store.getModel(key)
	assert.False(t, foundAfter)
}

func TestModelStore_CrossNamespaceIsolation(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: "ns-a", Name: "shared-model"},
		&externalModelInfo{refs: []*resolvedProviderRef{{provider: provider.OpenAI, weight: 1}}},
	)

	_, found := store.getModel(types.NamespacedName{Namespace: "ns-b", Name: "shared-model"})
	assert.False(t, found)
}
