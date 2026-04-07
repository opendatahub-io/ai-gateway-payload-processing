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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

func TestParseModelRefFromPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path     string
		wantNS   string
		wantName string
		wantOk   bool
	}{
		{path: "/llm/my-ref/v1/chat/completions", wantNS: "llm", wantName: "my-ref", wantOk: true},
		{path: "llm/my-ref/v1/chat/completions", wantNS: "llm", wantName: "my-ref", wantOk: true},
		{path: "/ns-a/model-b?stream=true", wantNS: "ns-a", wantName: "model-b", wantOk: true},
		{path: "//production//echo//v1/completions", wantOk: false}, // empty segment between slashes
		{path: "", wantOk: false},
		{path: "/only-one", wantOk: false},
		{path: "/v1/chat/completions", wantNS: "v1", wantName: "chat", wantOk: true},
	}
	for _, tt := range tests {
		tt := tt // capture range variable for t.Parallel subtests
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			got := parseMaaSModelRefKeyFromPath(tt.path)
			if !tt.wantOk {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantNS, got.Namespace)
			assert.Equal(t, tt.wantName, got.Name)
		})
	}
}

func TestProcessRequest_ModelResolved(t *testing.T) {
	store := newModelInfoStore()
	const (
		maasRefNS     = "llm"
		maasRefName   = "my-external-ref"
		extModel      = "claude-sonnet"
		credName      = "anthropic-key"
		credNamespace = "llm"
	)
	store.addOrUpdateMaaSModelRef(
		types.NamespacedName{Namespace: maasRefNS, Name: maasRefName},
		types.NamespacedName{Namespace: maasRefNS, Name: extModel},
	)
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: maasRefNS, Name: extModel},
		&externalModelInfo{
			provider:        provider.Anthropic,
			targetModel:     extModel,
			secretName:      credName,
			secretNamespace: credNamespace,
		},
	)

	plugin := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/" + maasRefNS + "/" + maasRefName + "/v1/chat/completions"
	// Body "model" must match targetModel on the ExternalModel (ProcessRequest validates this).
	req.Body["model"] = extModel

	err := plugin.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualModel, err := framework.ReadCycleStateKey[string](cs, state.ModelKey)
	assert.NoError(t, err)
	assert.Equal(t, extModel, actualModel)

	actualProvider, err := framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	assert.NoError(t, err)
	assert.Equal(t, provider.Anthropic, actualProvider)

	actualCredsName, err := framework.ReadCycleStateKey[string](cs, state.CredsRefName)
	assert.NoError(t, err)
	assert.Equal(t, credName, actualCredsName)

	actualCredsNamespace, err := framework.ReadCycleStateKey[string](cs, state.CredsRefNamespace)
	assert.NoError(t, err)
	assert.Equal(t, credNamespace, actualCredsNamespace)
}

func TestProcessRequest_ModelNotFound(t *testing.T) {
	store := newModelInfoStore()
	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/llm/unknown-ref/v1/chat/completions"
	req.Body["model"] = "unknown-model"

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.NoError(t, err)

	_, provErr := framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	assert.Error(t, provErr) // not found in CycleState
}

func TestProcessRequest_NoModel(t *testing.T) {
	store := newModelInfoStore()
	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()

	err := p.ProcessRequest(context.Background(), cs, framework.NewInferenceRequest())
	assert.NoError(t, err)

	// CycleState should remain empty — request passes through unmodified
	_, provErr := framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	assert.Error(t, provErr)
	_, modelErr := framework.ReadCycleStateKey[string](cs, state.ModelKey)
	assert.Error(t, modelErr)
}

func TestProcessRequest_BadPath(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateMaaSModelRef(
		types.NamespacedName{Namespace: "llm", Name: "ref"},
		types.NamespacedName{Namespace: "llm", Name: "ext"},
	)
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "llm", Name: "ext"},
		&externalModelInfo{provider: provider.OpenAI, secretName: "k", secretNamespace: "llm"},
	)
	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/incomplete"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.NoError(t, err)

	_, provErr := framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	assert.Error(t, provErr)
}

func TestProcessRequest_NoCredentialRef(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateMaaSModelRef(
		types.NamespacedName{Namespace: "llm", Name: "gpt-ref"},
		types.NamespacedName{Namespace: "llm", Name: "gpt-4o"},
	)
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "llm", Name: "gpt-4o"},
		&externalModelInfo{
			provider:    provider.OpenAI,
			targetModel: "gpt-4o", // reconciler sets this from ExternalModel metadata.name
			// no secret
		},
	)

	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/llm/gpt-ref/v1/chat/completions"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualProvider, _ := framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	assert.Equal(t, provider.OpenAI, actualProvider)

	_, credErr := framework.ReadCycleStateKey[string](cs, state.CredsRefName)
	assert.NoError(t, credErr)
	credsVal, _ := framework.ReadCycleStateKey[string](cs, state.CredsRefName)
	assert.Equal(t, "", credsVal)
}
