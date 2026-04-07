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

func TestModelStore_MaaSModelRefAndExternalModel(t *testing.T) {
	store := newModelInfoStore()
	maasKey := types.NamespacedName{Namespace: "ns", Name: "ref"}
	extKey := types.NamespacedName{Namespace: "ns", Name: "external-model"}

	store.addOrUpdateMaaSModelRef(maasKey, extKey)
	store.addOrUpdateExternalModel(extKey, &externalModelInfo{provider: provider.Anthropic})

	info, found := store.getModelInfo(maasKey)
	assert.True(t, found)
	assert.NotNil(t, info)
	assert.Equal(t, provider.Anthropic, info.provider)
}

func TestModelStore_DeleteMaaSModelRef(t *testing.T) {
	store := newModelInfoStore()
	maasKey := types.NamespacedName{Namespace: "ns", Name: "ref"}
	extKey := types.NamespacedName{Namespace: "ns", Name: "ext"}
	store.addOrUpdateMaaSModelRef(maasKey, extKey)
	store.addOrUpdateExternalModel(extKey, &externalModelInfo{provider: provider.OpenAI})

	store.deleteMaaSModelRef(maasKey)
	_, found := store.getModelInfo(maasKey)
	assert.False(t, found)
}

func TestModelStore_DeleteExternalModel(t *testing.T) {
	store := newModelInfoStore()
	maasKey := types.NamespacedName{Namespace: "ns", Name: "ref"}
	extKey := types.NamespacedName{Namespace: "ns", Name: "ext"}
	store.addOrUpdateMaaSModelRef(maasKey, extKey)
	store.addOrUpdateExternalModel(extKey, &externalModelInfo{provider: provider.OpenAI})

	store.deleteExternalModel(extKey)
	_, found := store.getModelInfo(maasKey)
	assert.False(t, found)
}
