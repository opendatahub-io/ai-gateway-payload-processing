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

package api_translation

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type testHandle struct{}

func (h *testHandle) Context() context.Context                { return context.Background() }
func (h *testHandle) Client() client.Client                   { return nil }
func (h *testHandle) ReconcilerBuilder() *ctrlbuilder.Builder { return nil }
func (h *testHandle) AddPlugin(_ string, _ plugin.Plugin)    {}
func (h *testHandle) Plugin(_ string) plugin.Plugin           { return nil }
func (h *testHandle) Datastore() datalayer.Datastore           { return nil }
func (h *testHandle) EventNotifier() datalayer.EventNotifier   { return nil }
func (h *testHandle) GetAllPlugins() []plugin.Plugin           { return nil }
func (h *testHandle) GetAllPluginsWithNames() map[string]plugin.Plugin { return nil }

func newTestPlugin() *APITranslationPlugin {
	p := NewAPITranslationPlugin(context.Background())
	return p
}

// ---------------------------------------------------------------------------
// Mock translator
// ---------------------------------------------------------------------------

type mockTranslator struct {
	reqBody     map[string]any
	reqHeaders  map[string]string
	reqRemove   []string
	reqErr      error
	respBody    map[string]any
	respErr     error
	lastReqBody map[string]any
	lastModel   string
}

func (m *mockTranslator) TranslateRequest(body map[string]any) (map[string]any, map[string]string, []string, error) {
	m.lastReqBody = body
	return m.reqBody, m.reqHeaders, m.reqRemove, m.reqErr
}

func (m *mockTranslator) TranslateResponse(body map[string]any, model string) (map[string]any, error) {
	m.lastModel = model
	return m.respBody, m.respErr
}

var _ translator.Translator = (*mockTranslator)(nil)

func newPluginWithMock(input, output apiformat.APIFormat, mock *mockTranslator) *APITranslationPlugin {
	return &APITranslationPlugin{
		typedName: plugin.TypedName{Type: APITranslationPluginType, Name: APITranslationPluginType},
		translators: map[translatorKey]translator.Translator{
			{input: input, output: output}: mock,
		},
	}
}

func newCycleStateWithFormats(input, output apiformat.APIFormat) *plugin.CycleState {
	cs := plugin.NewCycleState()
	cs.Write(state.InputAPIFormatKey, input)
	cs.Write(state.APIFormatKey, output)
	return cs
}

// ---------------------------------------------------------------------------
// Request: routing & passthrough
// ---------------------------------------------------------------------------

func TestProcessRequest_NoProvider(t *testing.T) {
	p := newTestPlugin()

	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "gpt-4o"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	assert.NoError(t, err)
	assert.False(t, req.BodyMutated())
}

func TestProcessRequest_OpenAIProvider(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "gpt-4o"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.NoError(t, err)
	assert.False(t, req.BodyMutated())
}

func TestProcessRequest_UnknownFormatCombination(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithFormats(apiformat.OpenAIResponses, apiformat.Messages)
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "some-model"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format combination")
}

// ---------------------------------------------------------------------------
// Request: mock translator — body, headers, authorization removal
// ---------------------------------------------------------------------------

func TestProcessRequest_BodyMutated(t *testing.T) {
	mock := &mockTranslator{
		reqBody:    map[string]any{"translated": true},
		reqHeaders: map[string]string{":path": "/v1/translated", "content-type": "application/json"},
		reqRemove:  []string{"x-custom"},
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	req := requesthandling.NewInferenceRequest()
	req.Headers["authorization"] = "Bearer sk-test"
	req.Headers["x-custom"] = "val"
	req.Body["model"] = "m"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	assert.True(t, req.BodyMutated())
	assert.Equal(t, true, req.Body["translated"])

	mutated := req.MutatedHeaders()
	assert.Equal(t, "/v1/translated", mutated[":path"])
	assert.Equal(t, "application/json", mutated["content-type"])

	removed := req.RemovedHeaders()
	assert.Contains(t, removed, "x-custom")
	assert.Contains(t, removed, "authorization", "authorization must always be removed")
}

func TestProcessRequest_NilBody_NoMutation(t *testing.T) {
	mock := &mockTranslator{
		reqBody:    nil,
		reqHeaders: map[string]string{":path": "/openai/v1/chat/completions"},
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	req := requesthandling.NewInferenceRequest()
	req.Headers["authorization"] = "Bearer sk-test"
	req.Body["model"] = "gpt-4o"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	assert.False(t, req.BodyMutated(), "nil translated body means no body mutation")

	mutated := req.MutatedHeaders()
	assert.Contains(t, mutated, ":path", "headers should still be applied even without body mutation")

	removed := req.RemovedHeaders()
	assert.Contains(t, removed, "authorization")
}

func TestProcessRequest_AuthorizationAlwaysRemoved(t *testing.T) {
	mock := &mockTranslator{
		reqRemove: nil,
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	req := requesthandling.NewInferenceRequest()
	req.Headers["authorization"] = "Bearer sk-test"
	req.Body["model"] = "m"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	removed := req.RemovedHeaders()
	assert.Contains(t, removed, "authorization",
		"authorization header must be removed even when translator doesn't request it")
}

func TestProcessRequest_TranslatorError(t *testing.T) {
	mock := &mockTranslator{
		reqErr: fmt.Errorf("model field is required"),
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	req := requesthandling.NewInferenceRequest()
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
	assert.Contains(t, err.Error(), "openai-chat")
}

func TestProcessRequest_ReceivesOriginalBody(t *testing.T) {
	mock := &mockTranslator{}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "my-model"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "test"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	assert.Equal(t, "my-model", mock.lastReqBody["model"])
}

// ---------------------------------------------------------------------------
// Response: routing & passthrough
// ---------------------------------------------------------------------------

func TestProcessResponse_NoProviderPassthrough(t *testing.T) {
	p := newTestPlugin()

	resp := requesthandling.NewInferenceResponse()
	resp.Body["object"] = "chat.completion"
	resp.Body["choices"] = []any{
		map[string]any{"message": map[string]any{"content": "hi"}},
	}

	err := p.ProcessResponse(context.Background(), plugin.NewCycleState(), resp)
	assert.NoError(t, err)
	assert.False(t, resp.BodyMutated())
}

func TestProcessResponse_OpenAIPassthrough(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["object"] = "chat.completion"

	err := p.ProcessResponse(context.Background(), cs, resp)
	assert.NoError(t, err)
	assert.False(t, resp.BodyMutated())
}

// ---------------------------------------------------------------------------
// Response: mock translator — body mutation, nil body, model, errors
// ---------------------------------------------------------------------------

func TestProcessResponse_BodyMutated(t *testing.T) {
	mock := &mockTranslator{
		respBody: map[string]any{
			"object":  "chat.completion",
			"model":   "test-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": "translated"}}},
		},
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	cs.Write(state.ModelKey, "test-model")

	resp := requesthandling.NewInferenceResponse()
	resp.Body["original"] = "data"

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.True(t, resp.BodyMutated())
	assert.Equal(t, "chat.completion", resp.Body["object"])
}

func TestProcessResponse_NilBody_NoMutation(t *testing.T) {
	mock := &mockTranslator{
		respBody: nil,
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["object"] = "chat.completion"

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.False(t, resp.BodyMutated(), "nil translated body means no body mutation")
}

func TestProcessResponse_ModelPassedToTranslator(t *testing.T) {
	mock := &mockTranslator{}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	cs.Write(state.ModelKey, "my-model-name")

	resp := requesthandling.NewInferenceResponse()
	resp.Body["data"] = "something"

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.Equal(t, "my-model-name", mock.lastModel,
		"model from CycleState must be passed to TranslateResponse")
}

func TestProcessResponse_TranslatorError(t *testing.T) {
	mock := &mockTranslator{
		respErr: fmt.Errorf("malformed response"),
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["data"] = "something"

	err := p.ProcessResponse(context.Background(), cs, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "malformed response")
	assert.Contains(t, err.Error(), "openai-chat")
}

func TestProcessResponse_UnknownFormatCombination(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithFormats(apiformat.OpenAIResponses, apiformat.Messages)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["data"] = "something"

	err := p.ProcessResponse(context.Background(), cs, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format combination")
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

func TestFactory_Success(t *testing.T) {
	p, err := APITranslationFactory("test-instance", nil, &testHandle{})
	require.NoError(t, err)
	assert.Equal(t, "test-instance", p.TypedName().Name)
	assert.Equal(t, APITranslationPluginType, p.TypedName().Type)
}

func TestProcessRequest_OpenAIWithPathOverride(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	cs.Write(state.PathKey, "/maas-default-gateway/v1/chat/completions")

	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "llama-4-scout"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}
	req.Headers["authorization"] = "Bearer sk-test"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err, "openai provider with path override must be supported without error")

	mutated := req.MutatedHeaders()
	assert.Equal(t, "/maas-default-gateway/v1/chat/completions", mutated[":path"],
		"openai with path override should use the override path")

	removed := req.RemovedHeaders()
	assert.Contains(t, removed, "authorization")
}

func TestIsPassthrough(t *testing.T) {
	tests := []struct {
		name     string
		input    apiformat.APIFormat
		output   apiformat.APIFormat
		expected bool
	}{
		{"anthropic messages match → passthrough", apiformat.Messages, apiformat.Messages, true},
		{"openai-responses match → passthrough", apiformat.OpenAIResponses, apiformat.OpenAIResponses, true},
		{"format mismatch → translate", apiformat.OpenAIChatCompletions, apiformat.Messages, false},
		{"openai-chat excluded even when matching → translate", apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, false},
		{"empty input → translate", "", apiformat.Messages, false},
		{"empty output → translate", apiformat.Messages, "", false},
		{"both empty → translate", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := plugin.NewCycleState()
			if tt.input != "" {
				cs.Write(state.InputAPIFormatKey, tt.input)
			}
			if tt.output != "" {
				cs.Write(state.APIFormatKey, tt.output)
			}
			assert.Equal(t, tt.expected, isPassthrough(cs))
		})
	}
}

// ---------------------------------------------------------------------------
// Path override from CycleState
// ---------------------------------------------------------------------------

func TestProcessRequest_PathOverrideFromCycleState(t *testing.T) {
	mock := &mockTranslator{
		reqHeaders: map[string]string{":path": "/v1/chat/completions"},
	}
	p := newPluginWithMock(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions, mock)

	cs := newCycleStateWithFormats(apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions)
	cs.Write(state.PathKey, "/maas-default-gateway/v1/chat/completions")

	req := requesthandling.NewInferenceRequest()
	req.Headers["authorization"] = "Bearer sk-test"
	req.Body["model"] = "llama-4-scout"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	assert.Equal(t, "/maas-default-gateway/v1/chat/completions", mutated[":path"],
		"path override from CycleState must replace translator's :path")
}

func TestProcessRequest_PathOverrideInPassthrough(t *testing.T) {
	p := NewAPITranslationPlugin(context.Background())
	cs := plugin.NewCycleState()
	cs.Write(state.ProviderKey, "anthropic")
	cs.Write(state.InputAPIFormatKey, apiformat.Messages)
	cs.Write(state.APIFormatKey, apiformat.Messages)
	cs.Write(state.PathKey, "/custom-passthrough-path")

	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "claude-opus-4-6"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
	req.Headers["authorization"] = "Bearer sk-test"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	mutated := req.MutatedHeaders()
	assert.Equal(t, "/custom-passthrough-path", mutated[":path"],
		"path override must apply even in passthrough mode")
}

func TestPassthrough_SkipsRequestTranslation(t *testing.T) {
	p := NewAPITranslationPlugin(context.Background())
	cs := plugin.NewCycleState()
	cs.Write(state.ProviderKey, "anthropic")
	cs.Write(state.InputAPIFormatKey, apiformat.Messages)
	cs.Write(state.APIFormatKey, apiformat.Messages)

	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "claude-opus-4-6"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
	req.Headers["authorization"] = "Bearer sk-test"

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	// Authorization should be removed even in passthrough
	_, hasAuth := req.Headers["authorization"]
	assert.False(t, hasAuth)

	// Body should NOT be translated (still Anthropic format, not OpenAI)
	_, hasMessages := req.Body["messages"]
	assert.True(t, hasMessages)
}

func TestPassthrough_SkipsResponseTranslation(t *testing.T) {
	p := NewAPITranslationPlugin(context.Background())
	cs := plugin.NewCycleState()
	cs.Write(state.ProviderKey, "anthropic")
	cs.Write(state.InputAPIFormatKey, apiformat.Messages)
	cs.Write(state.APIFormatKey, apiformat.Messages)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["type"] = "message"
	resp.Body["content"] = []any{map[string]any{"type": "text", "text": "hello"}}

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	// Body should NOT be translated (still Anthropic format, not converted to OpenAI)
	assert.Equal(t, "message", resp.Body["type"])
}
