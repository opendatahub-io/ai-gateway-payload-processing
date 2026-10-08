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

func TestModelStore_AddAndGetByName(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("claude-opus-4-8", &externalModelInfo{
		modelName: "claude-opus-4-8",
		refs:      []*resolvedProviderRef{{provider: provider.Anthropic, weight: 1}},
	})

	info, found := store.getModelByName("claude-opus-4-8")
	assert.True(t, found)
	assert.NotNil(t, info)
	assert.Equal(t, provider.Anthropic, info.refs[0].provider)
}

func TestModelStore_GetByName_NotFound(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("claude-opus-4-8", &externalModelInfo{
		modelName: "claude-opus-4-8",
		refs:      []*resolvedProviderRef{{provider: provider.OpenAI, weight: 1}},
	})

	_, found := store.getModelByName("gpt-5.5")
	assert.False(t, found)
}

func TestModelStore_DeleteByOwner(t *testing.T) {
	store := newInfoStore()
	owner := types.NamespacedName{Namespace: "models", Name: "model"}
	for _, name := range []string{"old-alias", "current-alias"} {
		store.addOrUpdateModel(name, &externalModelInfo{owner: owner, modelName: name})
	}
	other := &externalModelInfo{owner: types.NamespacedName{Namespace: "other", Name: owner.Name}, modelName: "other-alias"}
	store.addOrUpdateModel(other.modelName, other)

	store.deleteModel(owner)
	for _, name := range []string{"old-alias", "current-alias"} {
		_, found := store.getModelByName(name)
		assert.False(t, found, "all aliases owned by the deleted resource must be removed")
	}
	info, found := store.getModelByName(other.modelName)
	assert.True(t, found)
	assert.Same(t, other, info)
}

func TestModelStore_UniqueByModelName(t *testing.T) {
	store := newInfoStore()
	owner := types.NamespacedName{Namespace: "models", Name: "first"}
	store.addOrUpdateModel("shared-model", &externalModelInfo{
		owner:     owner,
		modelName: "shared-model",
		refs:      []*resolvedProviderRef{{provider: provider.OpenAI, weight: 1}},
	})

	// Same modelName overwrites — no namespace isolation
	store.addOrUpdateModel("shared-model", &externalModelInfo{
		owner:     types.NamespacedName{Namespace: owner.Namespace, Name: "second"},
		modelName: "shared-model",
		refs:      []*resolvedProviderRef{{provider: provider.Anthropic, weight: 1}},
	})

	info, found := store.getModelByName("shared-model")
	assert.True(t, found)
	assert.Equal(t, provider.Anthropic, info.refs[0].provider, "last write wins")

	store.deleteModel(owner)
	after, found := store.getModelByName("shared-model")
	assert.True(t, found, "the replaced owner must not delete the current owner's mapping")
	assert.Same(t, info, after)
}
