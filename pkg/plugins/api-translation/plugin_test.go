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
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type testHandle struct{}

func (h *testHandle) Context() context.Context                { return context.Background() }
func (h *testHandle) Client() client.Client                   { return nil }
func (h *testHandle) ReconcilerBuilder() *ctrlbuilder.Builder { return nil }

func newTestPlugin() *APITranslationPlugin {
	p, _ := NewAPITranslationPlugin(context.Background(), apiTranslationConfig{})
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

func newPluginWithMock(providerName string, mock *mockTranslator) *APITranslationPlugin {
	return &APITranslationPlugin{
		typedName: plugin.TypedName{Type: APITranslationPluginType, Name: APITranslationPluginType},
		providers: map[string]translator.Translator{
			providerName: mock,
		},
	}
}

func newCycleStateWithProvider(providerName string) *framework.CycleState {
	cs := framework.NewCycleState()
	cs.Write(state.ProviderKey, providerName)
	return cs
}

// ---------------------------------------------------------------------------
// Request: routing & passthrough
// ---------------------------------------------------------------------------

func TestProcessRequest_NoProvider(t *testing.T) {
	p := newTestPlugin()

	req := framework.NewInferenceRequest()
	req.Body["model"] = "gpt-4o"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), framework.NewCycleState(), req)
	assert.NoError(t, err)
	assert.False(t, req.BodyMutated())
}

func TestProcessRequest_OpenAIProvider(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithProvider("openai")
	req := framework.NewInferenceRequest()
	req.Body["model"] = "gpt-4o"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.NoError(t, err)
	assert.False(t, req.BodyMutated())
}

func TestProcessRequest_UnknownProvider(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithProvider("unknown")
	req := framework.NewInferenceRequest()
	req.Body["model"] = "some-model"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider")
	assert.Contains(t, err.Error(), "unknown")
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	req := framework.NewInferenceRequest()
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	req := framework.NewInferenceRequest()
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	req := framework.NewInferenceRequest()
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	req := framework.NewInferenceRequest()
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := p.ProcessRequest(context.Background(), cs, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
	assert.Contains(t, err.Error(), "test-provider")
}

func TestProcessRequest_ReceivesOriginalBody(t *testing.T) {
	mock := &mockTranslator{}
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	req := framework.NewInferenceRequest()
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

	resp := framework.NewInferenceResponse()
	resp.Body["object"] = "chat.completion"
	resp.Body["choices"] = []any{
		map[string]any{"message": map[string]any{"content": "hi"}},
	}

	err := p.ProcessResponse(context.Background(), framework.NewCycleState(), resp)
	assert.NoError(t, err)
	assert.False(t, resp.BodyMutated())
}

func TestProcessResponse_OpenAIPassthrough(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithProvider("openai")

	resp := framework.NewInferenceResponse()
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	cs.Write(state.ModelKey, "test-model")

	resp := framework.NewInferenceResponse()
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")

	resp := framework.NewInferenceResponse()
	resp.Body["object"] = "chat.completion"

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	assert.False(t, resp.BodyMutated(), "nil translated body means no body mutation")
}

func TestProcessResponse_ModelPassedToTranslator(t *testing.T) {
	mock := &mockTranslator{}
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")
	cs.Write(state.ModelKey, "my-model-name")

	resp := framework.NewInferenceResponse()
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
	p := newPluginWithMock("test-provider", mock)

	cs := newCycleStateWithProvider("test-provider")

	resp := framework.NewInferenceResponse()
	resp.Body["data"] = "something"

	err := p.ProcessResponse(context.Background(), cs, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "malformed response")
	assert.Contains(t, err.Error(), "test-provider")
}

func TestProcessResponse_UnknownProvider(t *testing.T) {
	p := newTestPlugin()

	cs := newCycleStateWithProvider("unknown")

	resp := framework.NewInferenceResponse()
	resp.Body["data"] = "something"

	err := p.ProcessResponse(context.Background(), cs, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider")
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
