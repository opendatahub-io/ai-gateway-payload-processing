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

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

func TestProcessRequest_ModelResolved(t *testing.T) {
	store := newInfoStore()
	const (
		extNS       = "llm"
		extName     = "claude-sonnet"
		targetModel = "claude-sonnet-1234"
		credName    = "anthropic-key"
	)
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: extNS, Name: extName},
		&externalModelInfo{refs: []resolvedProviderRef{{
			provider:        provider.Anthropic,
			targetModel:     targetModel,
			apiFormat:       apiformat.Messages,
			secretName:      credName,
			secretNamespace: extNS,
			config:          map[string]string{},
			weight:          1,
		}}},
	)

	plugin := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/" + extNS + "/" + extName + "/v1/chat/completions"
	req.Body["model"] = targetModel

	err := plugin.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualModel, err := framework.ReadCycleStateKey[string](cs, state.ModelKey)
	require.NoError(t, err)
	require.Equal(t, targetModel, actualModel)

	actualProvider, err := framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.NoError(t, err)
	require.Equal(t, provider.Anthropic, actualProvider)

	actualCredsName, err := framework.ReadCycleStateKey[string](cs, state.CredsRefName)
	require.NoError(t, err)
	require.Equal(t, credName, actualCredsName)

	actualCredsNamespace, err := framework.ReadCycleStateKey[string](cs, state.CredsRefNamespace)
	require.NoError(t, err)
	require.Equal(t, extNS, actualCredsNamespace)

	actualAPIFormat, err := framework.ReadCycleStateKey[apiformat.APIFormat](cs, state.APIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.Messages, actualAPIFormat)
}

func TestProcessRequest_ModelNotFound(t *testing.T) {
	store := newInfoStore()
	p := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/model-ns/model-name/v1/chat/completions"
	req.Body["model"] = "unknown-model"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
}

func TestProcessRequest_NoModel(t *testing.T) {
	store := newInfoStore()
	p := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()

	err := p.ProcessRequest(context.Background(), cs, framework.NewInferenceRequest())
	require.NoError(t, err)

	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
	_, err = framework.ReadCycleStateKey[string](cs, state.ModelKey)
	require.Error(t, err)
}

func TestProcessRequest_BadPath(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: "llm", Name: "ext"},
		&externalModelInfo{refs: []resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-4o",
			secretName: "k", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)
	p := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/incomplete"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
}

func TestSelectByWeight_SingleRef(t *testing.T) {
	refs := []resolvedProviderRef{
		{provider: "openai", weight: 1},
	}
	selected := selectByWeight(refs)
	require.Equal(t, "openai", selected.provider)
}

func TestSelectByWeight_Distribution(t *testing.T) {
	refs := []resolvedProviderRef{
		{provider: "openai", weight: 80},
		{provider: "anthropic", weight: 20},
	}

	counts := map[string]int{}
	for range 1000 {
		selected := selectByWeight(refs)
		counts[selected.provider]++
	}

	require.Greater(t, counts["openai"], 700, "openai should get majority of traffic")
	require.Greater(t, counts["anthropic"], 100, "anthropic should get some traffic")
}

func TestSelectByWeight_EqualWeights(t *testing.T) {
	refs := []resolvedProviderRef{
		{provider: "a", weight: 1},
		{provider: "b", weight: 1},
		{provider: "c", weight: 1},
	}

	counts := map[string]int{}
	for range 900 {
		selected := selectByWeight(refs)
		counts[selected.provider]++
	}

	for _, p := range []string{"a", "b", "c"} {
		require.Greater(t, counts[p], 200, "%s should get roughly equal traffic", p)
	}
}

func TestProcessRequest_AnthropicMessages(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: "llm", Name: "claude"},
		&externalModelInfo{refs: []resolvedProviderRef{{
			provider: provider.Anthropic, targetModel: "claude-opus-4-6",
			apiFormat: "messages", secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	p := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/llm/claude/v1/messages"
	req.Body["model"] = "claude-opus-4-6"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	inputFmt, err := framework.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.Messages, inputFmt)

	apiFormat, err := framework.ReadCycleStateKey[apiformat.APIFormat](cs, state.APIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.Messages, apiFormat)
}

func TestProcessRequest_OpenAIResponses(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: "llm", Name: "gpt"},
		&externalModelInfo{refs: []resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-5.5",
			apiFormat: "openai-chat", secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	p := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/llm/gpt/v1/responses"
	req.Body["model"] = "gpt-5.5"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	inputFmt, err := framework.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.OpenAIResponses, inputFmt)
}

func TestProcessRequest_UnsupportedPath(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel(
		types.NamespacedName{Namespace: "llm", Name: "model"},
		&externalModelInfo{refs: []resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-4o",
			apiFormat: "openai-chat", secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	p := &ModelProviderResolverPlugin{store: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/llm/model/v1/unknown"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported API path")
}

func TestDetectInputAPIFormat(t *testing.T) {
	tests := []struct {
		path     string
		expected apiformat.APIFormat
	}{
		{"llm/model/v1/chat/completions", apiformat.OpenAIChatCompletions},
		{"llm/model/v1/messages", apiformat.Messages},
		{"llm/model/v1/responses", apiformat.OpenAIResponses},
		{"llm/model/v1/unknown", ""},
		{"llm/model/v1/models", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := detectInputAPIFormat(tt.path)
			require.Equal(t, tt.expected, result)
		})
	}
}
