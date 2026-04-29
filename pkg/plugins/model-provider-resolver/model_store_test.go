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

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
)

func TestModelStore_AddAndGetExternalModel(t *testing.T) {
	store := newModelInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "gpt4"}

	store.addOrUpdateExternalModel(key, &externalModelInfo{
		modelName: "gpt4",
		refs: []providerRef{
			{provider: provider.Anthropic, targetModel: "claude-3", weight: 1},
		},
	})

	info, found := store.getModelInfo(key)
	assert.True(t, found)
	assert.NotNil(t, info)
	assert.Equal(t, "gpt4", info.modelName)
	require.Len(t, info.refs, 1)
	assert.Equal(t, provider.Anthropic, info.refs[0].provider)
}

func TestModelStore_GetModelInfo_NotFound(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "ns", Name: "ext"},
		&externalModelInfo{modelName: "ext", refs: []providerRef{{provider: provider.OpenAI, weight: 1}}},
	)

	_, found := store.getModelInfo(types.NamespacedName{Namespace: "ns", Name: "other"})
	assert.False(t, found)
}

func TestModelStore_DeleteExternalModel(t *testing.T) {
	store := newModelInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "ext"}
	store.addOrUpdateExternalModel(key, &externalModelInfo{modelName: "ext", refs: []providerRef{{provider: provider.OpenAI, weight: 1}}})

	_, foundBefore := store.getModelInfo(key)
	assert.True(t, foundBefore)

	store.deleteExternalModel(key)
	_, foundAfter := store.getModelInfo(key)
	assert.False(t, foundAfter)
}

func TestSelectProvider_SingleRef(t *testing.T) {
	info := &externalModelInfo{
		modelName:   "gpt4",
		totalWeight: 1,
		refs: []providerRef{
			{providerName: "my-openai", provider: "openai", targetModel: "gpt-4o", weight: 1},
		},
	}
	selected := info.selectProvider()
	assert.Equal(t, "my-openai", selected.providerName)
	assert.Equal(t, "gpt-4o", selected.targetModel)
}

func TestSelectProvider_WeightedDistribution(t *testing.T) {
	info := &externalModelInfo{
		modelName:   "gpt4",
		totalWeight: 100,
		refs: []providerRef{
			{providerName: "openai", weight: 80},
			{providerName: "bedrock", weight: 20},
		},
	}

	counts := map[string]int{}
	for i := 0; i < 10000; i++ {
		selected := info.selectProvider()
		counts[selected.providerName]++
	}

	// With 80/20 weights over 10000 iterations, OpenAI should get ~8000 (±500)
	assert.InDelta(t, 8000, counts["openai"], 500, "openai should get ~80%% of traffic")
	assert.InDelta(t, 2000, counts["bedrock"], 500, "bedrock should get ~20%% of traffic")
}

func TestSelectProvider_ZeroWeight(t *testing.T) {
	info := &externalModelInfo{
		modelName:   "gpt4",
		totalWeight: 10,
		refs: []providerRef{
			{providerName: "active", weight: 10},
			{providerName: "disabled", weight: 0},
		},
	}

	for i := 0; i < 100; i++ {
		selected := info.selectProvider()
		assert.Equal(t, "active", selected.providerName, "zero-weight provider should never be selected")
	}
}
