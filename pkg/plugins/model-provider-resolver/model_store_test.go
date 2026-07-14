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

func TestModelStore_DeleteByName(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("claude-opus-4-8", &externalModelInfo{
		modelName: "claude-opus-4-8",
		refs:      []*resolvedProviderRef{{provider: provider.OpenAI, weight: 1}},
	})

	_, foundBefore := store.getModelByName("claude-opus-4-8")
	assert.True(t, foundBefore)

	store.deleteModel("claude-opus-4-8")
	_, foundAfter := store.getModelByName("claude-opus-4-8")
	assert.False(t, foundAfter)
}

func TestModelStore_UniqueByModelName(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("shared-model", &externalModelInfo{
		modelName: "shared-model",
		refs:      []*resolvedProviderRef{{provider: provider.OpenAI, weight: 1}},
	})

	// Same modelName overwrites — no namespace isolation
	store.addOrUpdateModel("shared-model", &externalModelInfo{
		modelName: "shared-model",
		refs:      []*resolvedProviderRef{{provider: provider.Anthropic, weight: 1}},
	})

	info, found := store.getModelByName("shared-model")
	assert.True(t, found)
	assert.Equal(t, provider.Anthropic, info.refs[0].provider, "last write wins")
}
