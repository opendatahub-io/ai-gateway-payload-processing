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
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

func TestProcessRequest_ModelResolved(t *testing.T) {
	store := newModelInfoStore()
	const (
		extNS       = "llm"
		extName     = "claude-sonnet"
		targetModel = "claude-sonnet-1234"
		credName    = "anthropic-key"
	)
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: extNS, Name: extName},
		&externalModelInfo{
			provider:        provider.Anthropic,
			targetModel:     targetModel,
			secretName:      credName,
			secretNamespace: extNS,
		},
	)

	plugin := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/" + extNS + "/" + extName + "/v1/chat/completions"
	req.Headers["x-ai-gateway-request-namespace"] = extNS
	// Body "model" must match targetModel on the ExternalModel (ProcessRequest validates this).
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
}

func TestProcessRequest_ModelNotFound(t *testing.T) {
	store := newModelInfoStore()
	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/model-ns/model-name/v1/chat/completions"
	req.Headers["x-ai-gateway-request-namespace"] = "model-ns"
	req.Body["model"] = "unknown-model"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err) // not found in CycleState
}

func TestProcessRequest_NoModel(t *testing.T) {
	store := newModelInfoStore()
	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()

	err := p.ProcessRequest(context.Background(), cs, framework.NewInferenceRequest())
	require.NoError(t, err)

	// CycleState should remain empty — request passes through unmodified
	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
	_, err = framework.ReadCycleStateKey[string](cs, state.ModelKey)
	require.Error(t, err)
}

func TestProcessRequest_BadPath(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "llm", Name: "ext"},
		&externalModelInfo{provider: provider.OpenAI, targetModel: "gpt-4o", secretName: "k", secretNamespace: "llm"},
	)
	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/incomplete"
	req.Headers["x-ai-gateway-request-namespace"] = "llm"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
}

func TestProcessRequest_RejectsCrossNamespaceAccess(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "team-a", Name: "gpt4"},
		&externalModelInfo{
			provider:        provider.OpenAI,
			targetModel:     "gpt-4o",
			secretName:      "openai-key",
			secretNamespace: "team-a",
		},
	)

	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/team-a/gpt4/v1/chat/completions"
	req.Headers["x-ai-gateway-request-namespace"] = "team-b"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)

	commErr, ok := err.(errcommon.Error)
	require.True(t, ok)
	require.Equal(t, errcommon.Forbidden, commErr.Code)
	require.Contains(t, commErr.Msg, "cross-namespace access denied")
}

func TestProcessRequest_RejectsWhenRequestNamespaceMissing(t *testing.T) {
	store := newModelInfoStore()
	store.addOrUpdateExternalModel(
		types.NamespacedName{Namespace: "team-a", Name: "gpt4"},
		&externalModelInfo{
			provider:        provider.OpenAI,
			targetModel:     "gpt-4o",
			secretName:      "openai-key",
			secretNamespace: "team-a",
		},
	)

	p := &ModelProviderResolverPlugin{modelInfoStore: store}
	cs := framework.NewCycleState()
	req := framework.NewInferenceRequest()
	req.Headers[":path"] = "/team-a/gpt4/v1/chat/completions"
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)

	commErr, ok := err.(errcommon.Error)
	require.True(t, ok)
	require.Equal(t, errcommon.Forbidden, commErr.Code)
	require.Contains(t, commErr.Msg, "unable to validate namespace boundary")
}

func TestExtractRequestNamespace(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
		ok      bool
	}{
		{
			name: "direct namespace header",
			headers: map[string]string{
				"x-ai-gateway-request-namespace": "team-a",
			},
			want: "team-a",
			ok:   true,
		},
		{
			name: "spiffe namespace from xfcc",
			headers: map[string]string{
				"x-forwarded-client-cert": `By=spiffe://cluster.local/ns/istio-system/sa/ztunnel;Hash=abc;URI=spiffe://cluster.local/ns/team-b/sa/default`,
			},
			want: "team-b",
			ok:   true,
		},
		{
			name: "hint header ignored when same as spiffe namespace",
			headers: map[string]string{
				"x-forwarded-client-cert":        `URI=spiffe://cluster.local/ns/team-b/sa/default`,
				"x-ai-gateway-request-namespace": "team-b",
			},
			want: "team-b",
			ok:   true,
		},
		{
			name: "reject when hint header disagrees with spiffe namespace",
			headers: map[string]string{
				"x-forwarded-client-cert":        `URI=spiffe://cluster.local/ns/team-b/sa/default`,
				"x-ai-gateway-request-namespace": "team-a",
			},
			want: "",
			ok:   false,
		},
		{
			name: "fallback to hint headers when xfcc has no workload spiffe ns",
			headers: map[string]string{
				"x-forwarded-client-cert":        "By=spiffe://cluster.local/ns/istio-system/sa/ztunnel;Hash=abc",
				"x-ai-gateway-request-namespace": "team-a",
			},
			want: "team-a",
			ok:   true,
		},
		{
			name: "no namespace data",
			headers: map[string]string{
				"x-forwarded-client-cert": "By=spiffe://cluster.local/ns/istio-system/sa/ztunnel",
			},
			want: "",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractRequestNamespace(tt.headers)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
