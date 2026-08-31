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
	"encoding/json"
	"testing"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
	maas_headers_guard "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/maas-headers-guard"
)

func TestProcessRequest_ModelResolved(t *testing.T) {
	store := newInfoStore()
	const (
		extNS       = "llm"
		extName     = "claude-sonnet"
		targetModel = "claude-sonnet-1234"
		credName    = "anthropic-key"
		endpoint    = "api.anthropic.com"
	)
	store.addOrUpdateModel(extName,
		&externalModelInfo{modelName: extName, refs: []*resolvedProviderRef{{
			provider:        provider.Anthropic,
			targetModel:     targetModel,
			apiFormat:       apiformat.Messages,
			auth:            auth.APIKey,
			endpoint:        endpoint,
			secretName:      credName,
			secretNamespace: extNS,
			config:          map[string]string{},
			weight:          1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/" + extNS + "/" + extName + "/v1/chat/completions"
	req.Body["model"] = extName

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualModel, err := plugin.ReadCycleStateKey[string](cs, state.ModelKey)
	require.NoError(t, err)
	require.Equal(t, targetModel, actualModel)

	actualProvider, err := plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.NoError(t, err)
	require.Equal(t, provider.Anthropic, actualProvider)

	actualCredsName, err := plugin.ReadCycleStateKey[string](cs, state.CredsRefName)
	require.NoError(t, err)
	require.Equal(t, credName, actualCredsName)

	actualCredsNamespace, err := plugin.ReadCycleStateKey[string](cs, state.CredsRefNamespace)
	require.NoError(t, err)
	require.Equal(t, extNS, actualCredsNamespace)

	actualAPIFormat, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.APIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.Messages, actualAPIFormat)

	actualAuthType, err := plugin.ReadCycleStateKey[auth.Auth](cs, state.AuthTypeKey)
	require.NoError(t, err)
	require.Equal(t, auth.APIKey, actualAuthType)

	actualEndpoint, err := plugin.ReadCycleStateKey[string](cs, state.EndpointKey)
	require.NoError(t, err)
	require.Equal(t, endpoint, actualEndpoint)
}

func TestProcessRequest_PathWrittenToCycleState(t *testing.T) {
	store := newInfoStore()
	const (
		extNS       = "llm"
		extName     = "remote-llama"
		targetModel = "llama-4-scout"
		credName    = "cluster-b-key"
		endpoint    = "maas.cluster-b.example.com"
		path        = "/maas-default-gateway/v1/chat/completions"
	)
	store.addOrUpdateModel(extName,
		&externalModelInfo{modelName: extName, refs: []*resolvedProviderRef{{
			provider:        provider.OpenAI,
			targetModel:     targetModel,
			apiFormat:       apiformat.OpenAIChatCompletions,
			auth:            auth.APIKey,
			endpoint:        endpoint,
			path:            path,
			secretName:      credName,
			secretNamespace: extNS,
			config:          map[string]string{},
			weight:          1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/" + extNS + "/" + extName + "/v1/chat/completions"
	req.Body["model"] = extName

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualPath, err := plugin.ReadCycleStateKey[string](cs, state.PathKey)
	require.NoError(t, err)
	require.Equal(t, path, actualPath)
}

func TestProcessRequest_UnknownModelPassesThrough(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("gpt4",
		&externalModelInfo{modelName: "gpt4", refs: []*resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-4o",
			apiFormat:  apiformat.OpenAIChatCompletions,
			secretName: "k", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)
	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/gpt4/v1/chat/completions"
	req.Body["model"] = "unknown-model"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "unknown model name should pass through for internal models")

	_, provErr := plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, provErr, "provider should not be set for unknown models")
}

func TestProcessRequest_ModelNotFound(t *testing.T) {
	store := newInfoStore()
	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/model-ns/model-name/v1/chat/completions"
	req.Body["model"] = "unknown-model"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)

	inputFmt, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.NoError(t, err, "input format should be recorded even when no ExternalModel matches")
	require.Equal(t, apiformat.OpenAIChatCompletions, inputFmt)
}

func TestProcessRequest_NoModel(t *testing.T) {
	store := newInfoStore()
	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()

	err := instance.ProcessRequest(context.Background(), cs, requesthandling.NewInferenceRequest())
	require.NoError(t, err)

	_, err = plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
	_, err = plugin.ReadCycleStateKey[string](cs, state.ModelKey)
	require.Error(t, err)
}

func TestProcessRequest_BadPath(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("ext",
		&externalModelInfo{modelName: "ext", refs: []*resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-4o",
			secretName: "k", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)
	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/incomplete"
	req.Body["model"] = "gpt-4o"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)

	_, err = plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.Error(t, err, "input format should not be recorded for unrecognized paths")
}

func TestSelectByWeight_SingleRef(t *testing.T) {
	refs := []*resolvedProviderRef{
		{provider: "openai", weight: 1},
	}
	selected := selectByWeight(refs)
	require.Equal(t, "openai", selected.provider)
}

func TestSelectByWeight_Distribution(t *testing.T) {
	refs := []*resolvedProviderRef{
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
	refs := []*resolvedProviderRef{
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
	store.addOrUpdateModel("claude",
		&externalModelInfo{modelName: "claude", refs: []*resolvedProviderRef{{
			provider: provider.Anthropic, targetModel: "claude-opus-4-6",
			apiFormat: "messages", secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/claude/v1/messages"
	req.Body["model"] = "claude"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	inputFmt, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.Messages, inputFmt)

	apiFormat, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.APIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.Messages, apiFormat)
}

func TestProcessRequest_OpenAIResponses(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("gpt",
		&externalModelInfo{modelName: "gpt", refs: []*resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-5.5",
			apiFormat: "openai-chat", secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/gpt/v1/responses"
	req.Body["model"] = "gpt"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	inputFmt, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.OpenAIResponses, inputFmt)
}

func TestProcessRequest_OpenAIEmbeddings(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("bge-m3",
		&externalModelInfo{modelName: "bge-m3", refs: []*resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "bge-m3",
			apiFormat: apiformat.OpenAIEmbeddings, secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/bge-m3/v1/embeddings"
	req.Body["model"] = "bge-m3"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	inputFmt, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.InputAPIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.OpenAIEmbeddings, inputFmt)

	apiFormat, err := plugin.ReadCycleStateKey[apiformat.APIFormat](cs, state.APIFormatKey)
	require.NoError(t, err)
	require.Equal(t, apiformat.OpenAIEmbeddings, apiFormat)
}

func TestProcessRequest_UnsupportedPath(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("model",
		&externalModelInfo{modelName: "model", refs: []*resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-4o",
			apiFormat: "openai-chat", secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/model/v1/unknown"
	req.Body["model"] = "model"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported API endpoint")
}

func TestProcessRequest_LLMISvcPublisherIDBodyRewrite(t *testing.T) {
	const publisherID = "publishers/llm/models/facebook/opt-125m"
	const modelName = "facebook/opt-125m"

	store := newInfoStore()
	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/v1/chat/completions"
	req.Headers["x-gateway-model-name"] = publisherID
	req.Body["model"] = publisherID

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	require.Equal(t, modelName, req.Body["model"],
		"body model field should be rewritten to just the model name for vLLM")
	require.Equal(t, publisherID, req.Headers["x-gateway-model-name"],
		"X-Gateway-Model-Name header must not be modified — KServe routes on it")

	resolvedModel, err := plugin.ReadCycleStateKey[string](cs, state.ModelKey)
	require.NoError(t, err)
	require.Equal(t, publisherID, resolvedModel,
		"publisher ID should be written to CycleState for ipp-post metering and response restoration")

	_, provErr := plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, provErr, "no provider should be set for LLMISvc models")
}

func TestProcessRequest_LLMISvcPublisherIDPassThroughWhenMalformed(t *testing.T) {
	store := newInfoStore()
	instance := &ModelProviderResolverPlugin{store: store}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/v1/chat/completions"
	// No "/models/" segment — should pass through without rewriting
	req.Headers["x-gateway-model-name"] = "publishers/llm/nope"
	req.Body["model"] = "publishers/llm/nope"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "malformed publisher ID should pass through without error")
	require.Equal(t, "publishers/llm/nope", req.Body["model"],
		"malformed publisher ID body should not be rewritten")
}

// --- Loop detection tests (#343: X-Origin-Cluster) ---

func remoteMaaSRef() *resolvedProviderRef {
	return &resolvedProviderRef{
		provider: provider.RemoteMaaS, targetModel: "llama-4-scout",
		apiFormat: apiformat.OpenAIChatCompletions, auth: auth.APIKey,
		endpoint: "maas.cluster-b.example.com", path: "/maas/v1/chat/completions",
		secretName: "key", secretNamespace: "llm",
		config: map[string]string{}, weight: 1,
	}
}

func requireLoopForbidden(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, err.Error(), "routing loop detected")
	var commErr errcommon.Error
	require.ErrorAs(t, err, &commErr)
	require.Equal(t, errcommon.Forbidden, commErr.Code)
}

func TestProcessRequest_OriginCluster_Self_Blocked(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Headers[state.OriginClusterHeader] = "cluster-a"
	req.Body["model"] = "remote-llama"

	requireLoopForbidden(t, instance.ProcessRequest(context.Background(), cs, req))
}

func TestProcessRequest_OriginCluster_Self_FromCycleState_Blocked(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	cs.Write(state.OriginClusterKey, "cluster-a")
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Body["model"] = "remote-llama"

	requireLoopForbidden(t, instance.ProcessRequest(context.Background(), cs, req))
}

func TestProcessRequest_OriginCluster_Self_BeforeCycleStateWrite(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Headers[state.OriginClusterHeader] = "cluster-a"
	req.Body["model"] = "remote-llama"

	requireLoopForbidden(t, instance.ProcessRequest(context.Background(), cs, req))
	_, csErr := plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, csErr, "provider must not be written when the loop is rejected")
}

func TestProcessRequest_OriginCluster_Self_CaseInsensitive(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	instance := &ModelProviderResolverPlugin{store: store, clusterName: "Cluster-A"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Headers["X-Origin-Cluster"] = "CLUSTER-A"
	req.Body["model"] = "remote-llama"

	requireLoopForbidden(t, instance.ProcessRequest(context.Background(), cs, req))
}

func TestProcessRequest_OriginCluster_OtherCluster_AllowedABC(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	// Cluster B receives a request that originated on A and forwards to C.
	guardPlugin, err := maas_headers_guard.Factory("guard", nil, nil)
	require.NoError(t, err)
	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-b"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Headers[state.OriginClusterHeader] = "cluster-a"
	req.Body["model"] = "remote-llama"

	require.NoError(t, guardPlugin.(*maas_headers_guard.Plugin).ProcessRequest(context.Background(), cs, req))
	err = instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "A→B→C must not be rejected: origin is a different cluster")

	mutated := req.MutatedHeaders()
	require.Equal(t, "cluster-a", mutated[state.OriginClusterHeader],
		"original origin must be preserved, not overwritten with this cluster")
}

func TestProcessRequest_OriginCluster_InternalModel_PassesThrough(t *testing.T) {
	store := newInfoStore()
	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/local-model/v1/chat/completions"
	req.Headers[state.OriginClusterHeader] = "cluster-a"
	req.Body["model"] = "local-model"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "local models are not ExternalModels; loop check does not apply")
}

func TestProcessRequest_OriginCluster_UnsetClusterName_Disabled(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	instance := &ModelProviderResolverPlugin{store: store} // CLUSTER_NAME unset
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Headers[state.OriginClusterHeader] = "cluster-a"
	req.Body["model"] = "remote-llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "loop detection is disabled when CLUSTER_NAME is unset")
}

func TestProcessRequest_OriginCluster_InjectsOnRemoteMaaS(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Body["model"] = "remote-llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	require.Equal(t, "cluster-a", mutated[state.OriginClusterHeader],
		"first remote-maas hop must inject this cluster's name")
}

func TestProcessRequest_OriginCluster_DoesNotInjectOnThirdParty(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("gpt4",
		&externalModelInfo{modelName: "gpt4", refs: []*resolvedProviderRef{{
			provider: provider.OpenAI, targetModel: "gpt-4o",
			apiFormat: apiformat.OpenAIChatCompletions, auth: auth.APIKey,
			endpoint: "api.openai.com", path: "/v1/chat/completions",
			secretName: "key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		}}},
	)

	instance := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/gpt4/v1/chat/completions"
	req.Body["model"] = "gpt4"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	_, injected := mutated[state.OriginClusterHeader]
	require.False(t, injected, "direct third-party APIs must not receive X-Origin-Cluster even when path is set")
}

func TestProcessRequest_OriginCluster_GuardThenResolver_SpoofSelf(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("remote-llama", &externalModelInfo{modelName: "remote-llama", refs: []*resolvedProviderRef{remoteMaaSRef()}})

	guardPlugin, err := maas_headers_guard.Factory("guard", nil, nil)
	require.NoError(t, err)
	resolver := &ModelProviderResolverPlugin{store: store, clusterName: "cluster-a"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/remote-llama/v1/chat/completions"
	req.Headers["X-Origin-Cluster"] = "cluster-a"
	req.Body["model"] = "remote-llama"

	require.NoError(t, guardPlugin.(*maas_headers_guard.Plugin).ProcessRequest(context.Background(), cs, req))

	_, stillPresent := req.Headers["X-Origin-Cluster"]
	require.False(t, stillPresent, "guard must strip the client-supplied header")

	requireLoopForbidden(t, resolver.ProcessRequest(context.Background(), cs, req))
}

func TestProcessRequest_HubMode_Propose_DoesNotInjectOrigin(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: hubRefs()})
	instance := &ModelProviderResolverPlugin{store: store, hubMode: true, clusterName: "hub-cluster"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/v1/chat/completions"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	_, injected := mutated[state.OriginClusterHeader]
	require.False(t, injected, "hub PROPOSE is not an outbound hop")
}

func TestProcessRequest_HubMode_Transform_InjectsOriginOnSpokeHop(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: hubRefs()})
	instance := &ModelProviderResolverPlugin{store: store, hubMode: true, clusterName: "hub-cluster"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/v1/chat/completions"
	req.Headers["x-gateway-destination-endpoint"] = "maas.spoke-east.example.com"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	require.Equal(t, "hub-cluster", mutated[state.OriginClusterHeader],
		"hub TRANSFORM to a spoke is a cross-cluster hop and must tag origin")
}

func TestProcessRequest_HubMode_Transform_PreservesIncomingOrigin(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: hubRefs()})
	guardPlugin, err := maas_headers_guard.Factory("guard", nil, nil)
	require.NoError(t, err)
	instance := &ModelProviderResolverPlugin{store: store, hubMode: true, clusterName: "hub-cluster"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/v1/chat/completions"
	req.Headers["x-gateway-destination-endpoint"] = "maas.spoke-east.example.com"
	req.Headers[state.OriginClusterHeader] = "cluster-a"
	req.Body["model"] = "llama"

	require.NoError(t, guardPlugin.(*maas_headers_guard.Plugin).ProcessRequest(context.Background(), cs, req))
	err = instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	require.Equal(t, "cluster-a", mutated[state.OriginClusterHeader],
		"hub must preserve the first origin so a later hop back to A can be detected")
}

func TestProcessRequest_HubMode_Propose_SelfOrigin_Blocked(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: hubRefs()})
	instance := &ModelProviderResolverPlugin{store: store, hubMode: true, clusterName: "hub-cluster"}
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/v1/chat/completions"
	req.Headers[state.OriginClusterHeader] = "hub-cluster"
	req.Body["model"] = "llama"

	requireLoopForbidden(t, instance.ProcessRequest(context.Background(), cs, req))
}

func TestDetectInputAPIFormat(t *testing.T) {
	tests := []struct {
		path     string
		expected apiformat.APIFormat
	}{
		{"llm/model/v1/chat/completions", apiformat.OpenAIChatCompletions},
		{"llm/model/v1/embeddings", apiformat.OpenAIEmbeddings},
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

// --- Hub mode tests ---

const pseudoHeaderKey = "x-ipp-internal-dynamic-metadata"

func newHubModePlugin(store *infoStore) *ModelProviderResolverPlugin {
	return &ModelProviderResolverPlugin{store: store, hubMode: true}
}

func hubRefs() []*resolvedProviderRef {
	return []*resolvedProviderRef{
		{
			provider: provider.OpenAI, providerName: "spoke-east",
			targetModel: "llama-4-scout", apiFormat: apiformat.OpenAIChatCompletions,
			auth: auth.APIKey, endpoint: "maas.spoke-east.example.com",
			path: "/v1/chat/completions", secretName: "east-key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		},
		{
			provider: provider.OpenAI, providerName: "spoke-west",
			targetModel: "llama-4-scout", apiFormat: apiformat.OpenAIChatCompletions,
			auth: auth.APIKey, endpoint: "maas.spoke-west.example.com",
			path: "/v1/chat/completions", secretName: "west-key", secretNamespace: "llm",
			config: map[string]string{}, weight: 1,
		},
	}
}

func TestProcessRequest_HubMode_Propose(t *testing.T) {
	store := newInfoStore()
	refs := hubRefs()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: refs})

	instance := newHubModePlugin(store)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/llama/v1/chat/completions"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	raw, ok := req.MutatedHeaders()[pseudoHeaderKey]
	require.True(t, ok, "pseudo-header must be set in PROPOSE phase")

	var entry struct {
		Namespace string   `json:"ns"`
		Key       string   `json:"key"`
		Values    []string `json:"values"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &entry))
	assert.Equal(t, "envoy.lb.subset_hint", entry.Namespace)
	assert.ElementsMatch(t, []string{"maas.spoke-east.example.com", "maas.spoke-west.example.com"}, entry.Values)

	_, provErr := plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, provErr, "PROPOSE must not write CycleState")

	_, selHdr := req.MutatedHeaders()[SelectedProviderHeader]
	assert.False(t, selHdr, "PROPOSE must not set x-ipp-selected-provider")
}

func TestProcessRequest_HubMode_Propose_NoEligibleSpokes(t *testing.T) {
	store := newInfoStore()
	store.addOrUpdateModel("llama", &externalModelInfo{
		modelName: "llama",
		refs: []*resolvedProviderRef{
			{provider: provider.OpenAI, endpoint: "spoke.example.com", weight: 0, config: map[string]string{}},
		},
	})

	instance := newHubModePlugin(store)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/llama/v1/chat/completions"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, ok := req.MutatedHeaders()[pseudoHeaderKey]
	assert.False(t, ok, "no metadata when all weights are 0")
}

func TestProcessRequest_HubMode_Propose_ModelNotFound(t *testing.T) {
	store := newInfoStore()
	instance := newHubModePlugin(store)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/unknown/v1/chat/completions"
	req.Body["model"] = "unknown"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "unknown model should pass through")

	_, ok := req.MutatedHeaders()[pseudoHeaderKey]
	assert.False(t, ok, "no metadata for unknown model")
}

func TestProcessRequest_HubMode_Transform(t *testing.T) {
	store := newInfoStore()
	refs := hubRefs()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: refs})

	instance := newHubModePlugin(store)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/llama/v1/chat/completions"
	req.Headers["x-gateway-destination-endpoint"] = "maas.spoke-west.example.com"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualProvider, err := plugin.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.NoError(t, err)
	assert.Equal(t, provider.OpenAI, actualProvider)

	actualModel, err := plugin.ReadCycleStateKey[string](cs, state.ModelKey)
	require.NoError(t, err)
	assert.Equal(t, "llama-4-scout", actualModel)

	actualEndpoint, err := plugin.ReadCycleStateKey[string](cs, state.EndpointKey)
	require.NoError(t, err)
	assert.Equal(t, "maas.spoke-west.example.com", actualEndpoint)

	assert.Equal(t, "spoke-west", req.MutatedHeaders()[SelectedProviderHeader])
	assert.Equal(t, "maas.spoke-west.example.com", req.MutatedHeaders()["Host"])
}

func TestProcessRequest_HubMode_Transform_WithPort(t *testing.T) {
	store := newInfoStore()
	refs := hubRefs()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: refs})

	instance := newHubModePlugin(store)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/llama/v1/chat/completions"
	req.Headers["x-gateway-destination-endpoint"] = "maas.spoke-east.example.com:443"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	actualEndpoint, err := plugin.ReadCycleStateKey[string](cs, state.EndpointKey)
	require.NoError(t, err)
	assert.Equal(t, "maas.spoke-east.example.com", actualEndpoint)

	assert.Equal(t, "spoke-east", req.MutatedHeaders()[SelectedProviderHeader])
}

func TestProcessRequest_HubMode_Transform_NoMatch(t *testing.T) {
	store := newInfoStore()
	refs := hubRefs()
	store.addOrUpdateModel("llama", &externalModelInfo{modelName: "llama", refs: refs})

	instance := newHubModePlugin(store)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers[":path"] = "/llm/llama/v1/chat/completions"
	req.Headers["x-gateway-destination-endpoint"] = "unknown.spoke.example.com"
	req.Body["model"] = "llama"

	err := instance.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no ExternalProvider matches destination")
}

func TestFindRefByEndpoint(t *testing.T) {
	refs := []*resolvedProviderRef{
		{providerName: "east", endpoint: "maas.east.example.com", weight: 1},
		{providerName: "west", endpoint: "maas.west.example.com", weight: 1},
		{providerName: "disabled", endpoint: "maas.disabled.example.com", weight: 0},
	}

	tests := []struct {
		name        string
		destination string
		wantName    string
		wantNil     bool
	}{
		{"exact match", "maas.east.example.com", "east", false},
		{"with port", "maas.west.example.com:443", "west", false},
		{"no match", "maas.central.example.com", "", true},
		{"empty", "", "", true},
		{"disabled ref skipped", "maas.disabled.example.com", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findRefByEndpoint(refs, tt.destination)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.wantName, result.providerName)
			}
		})
	}
}

func TestStripPort(t *testing.T) {
	assert.Equal(t, "host.example.com", stripPort("host.example.com:443"))
	assert.Equal(t, "host.example.com", stripPort("host.example.com"))
	assert.Equal(t, "localhost", stripPort("localhost:8080"))
	assert.Equal(t, "", stripPort(""))
}
