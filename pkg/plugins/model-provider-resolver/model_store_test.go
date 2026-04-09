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
	store := newModelInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "external-model"}

	store.addOrUpdateExternalModel(key, &externalModelInfo{provider: provider.Anthropic})

	info, found := store.getModelInfo(key)
	assert.True(t, found)
	assert.NotNil(t, info)
	assert.Equal(t, provider.Anthropic, info.provider)
}

func TestModelStore_GetModelInfo_NotFound(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "ns", Name: "ext"},
		&externalModelInfo{provider: provider.OpenAI},
	)

	_, found := store.getModelInfo(types.NamespacedName{Namespace: "ns", Name: "other"})
	assert.False(t, found)
}

func TestModelStore_DeleteExternalModel(t *testing.T) {
	store := newModelInfoStore()
	key := types.NamespacedName{Namespace: "ns", Name: "ext"}
	store.addOrUpdateExternalModel(key, &externalModelInfo{provider: provider.OpenAI})

	_, foundBefore := store.getModelInfo(key)
	assert.True(t, foundBefore)

	store.deleteExternalModel(key)
	_, foundAfter := store.getModelInfo(key)
	assert.False(t, foundAfter)
}
