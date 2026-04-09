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
	req.Body["model"] = "gpt-4o"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, err = framework.ReadCycleStateKey[string](cs, state.ProviderKey)
	require.Error(t, err)
}
