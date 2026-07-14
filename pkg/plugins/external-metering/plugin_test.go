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

package external_metering

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

// --- Factory Tests ---

func TestFactory_ValidConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	config, _ := json.Marshal(map[string]any{
		"meteringURL":    srv.URL,
		"timeoutSeconds": 3,
		"featureKey":     "tokens",
		"source":         "test",
		"failOpen":       false,
	})

	p, err := ExternalMeteringFactory("test-metering", config, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "test-metering", p.TypedName().Name)
	assert.Equal(t, ExternalMeteringPluginType, p.TypedName().Type)
}

func TestFactory_MissingURL(t *testing.T) {
	config, _ := json.Marshal(map[string]any{})
	_, err := ExternalMeteringFactory("test", config, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "meteringURL")
}

func TestFactory_InvalidJSON(t *testing.T) {
	_, err := ExternalMeteringFactory("test", []byte("{invalid"), nil)
	require.Error(t, err)
}

func TestFactory_Defaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	config, _ := json.Marshal(map[string]any{"meteringURL": srv.URL})
	bp, err := ExternalMeteringFactory("test", config, nil)
	require.NoError(t, err)

	p := bp.(*ExternalMeteringPlugin)
	assert.Equal(t, defaultFeatureKey, p.featureKey)
	assert.Equal(t, defaultSource, p.source)
	assert.True(t, p.failOpen)
}

// --- ProcessRequest Tests ---

func TestProcessRequest_BalanceAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/api/v1/customers/alice/entitlements/inference-tokens/value")
		assert.Equal(t, "gpt-4o", r.URL.Query().Get("model"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hasAccess":true,"balance":9000,"usage":1000,"overage":0}`))
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	req := newTestRequest("alice", "finance", "premium", "gpt-4o", false)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	username, _ := plugin.ReadCycleStateKey[string](cs, state.MeteringUsernameKey)
	assert.Equal(t, "alice", username)
	group, _ := plugin.ReadCycleStateKey[string](cs, state.MeteringGroupKey)
	assert.Equal(t, "finance", group)
	sub, _ := plugin.ReadCycleStateKey[string](cs, state.MeteringSubscriptionKey)
	assert.Equal(t, "premium", sub)
	model, _ := plugin.ReadCycleStateKey[string](cs, state.MeteringModelKey)
	assert.Equal(t, "gpt-4o", model)
}

func TestProcessRequest_BalanceDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hasAccess":false,"balance":0,"usage":10000,"overage":0}`))
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	req := newTestRequest("alice", "", "", "gpt-4o", false)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)

	var commErr errcommon.Error
	require.ErrorAs(t, err, &commErr)
	assert.Equal(t, errcommon.ResourceExhausted, commErr.Code)
}

func TestProcessRequest_Unreachable_FailOpen(t *testing.T) {
	p := newTestPlugin(t, "http://127.0.0.1:1", true)
	cs := plugin.NewCycleState()
	req := newTestRequest("alice", "", "", "gpt-4o", false)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)
}

func TestProcessRequest_Unreachable_FailClosed(t *testing.T) {
	p := newTestPlugin(t, "http://127.0.0.1:1", false)
	cs := plugin.NewCycleState()
	req := newTestRequest("alice", "", "", "gpt-4o", false)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.Error(t, err)

	var commErr errcommon.Error
	require.ErrorAs(t, err, &commErr)
	assert.Equal(t, errcommon.ServiceUnavailable, commErr.Code)
}

func TestProcessRequest_MissingUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not call metering when username is missing")
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	req := newTestRequest("", "", "", "gpt-4o", false)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)
}

func TestProcessRequest_StreamingDoesNotInjectUsageOption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hasAccess":true,"balance":9000,"usage":1000,"overage":0}`))
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	req := newTestRequest("alice", "", "", "gpt-4o", true)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	// stream_options injection is handled by stream-usage-enforcer plugin, not metering
	_, exists := req.Body["stream_options"]
	assert.False(t, exists)
}

func TestProcessRequest_NonStreamingNoUsageOption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hasAccess":true,"balance":9000,"usage":1000,"overage":0}`))
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	req := newTestRequest("alice", "", "", "gpt-4o", false)

	err := p.ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, exists := req.Body["stream_options"]
	assert.False(t, exists)
}

// --- ProcessResponse Tests ---

func TestProcessResponse_ReportsUsage(t *testing.T) {
	var mu sync.Mutex
	var receivedEvent map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/events" {
			var evt map[string]any
			_ = json.NewDecoder(r.Body).Decode(&evt)
			mu.Lock()
			receivedEvent = evt
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringGroupKey, "finance")
	cs.Write(state.MeteringSubscriptionKey, "premium")
	cs.Write(state.MeteringModelKey, "gpt-4o")

	resp := newTestResponse(150, 80, 230)

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, receivedEvent)
	assert.Equal(t, "1.0", receivedEvent["specversion"])
	assert.Equal(t, "inference.tokens.used", receivedEvent["type"])
	assert.Equal(t, "alice", receivedEvent["subject"])
	assert.Equal(t, "maas-gateway", receivedEvent["source"])

	data, ok := receivedEvent["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", data["user"])
	assert.Equal(t, "finance", data["group"])
	assert.Equal(t, "premium", data["subscription"])
	assert.Equal(t, "gpt-4o", data["model"])
	assert.Equal(t, float64(150), data["prompt_tokens"])
	assert.Equal(t, float64(80), data["completion_tokens"])
	assert.Equal(t, float64(230), data["total_tokens"])
}

func TestProcessResponse_MissingUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatal("should not report when usage is missing")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")

	resp := requesthandling.NewInferenceResponse()
	resp.Body["model"] = "gpt-4o"
	resp.Body["choices"] = []any{}

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)
}

func TestProcessResponse_MissingUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatal("should not report when username is missing")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()

	resp := newTestResponse(100, 50, 150)

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)
}

func TestProcessResponse_ReportFailure_NoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringModelKey, "gpt-4o")

	resp := newTestResponse(100, 50, 150)

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)
}

func TestProcessResponse_AnthropicFormat(t *testing.T) {
	var mu sync.Mutex
	var receivedEvent map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/events" {
			var evt map[string]any
			_ = json.NewDecoder(r.Body).Decode(&evt)
			mu.Lock()
			receivedEvent = evt
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringModelKey, "claude-sonnet")

	resp := requesthandling.NewInferenceResponse()
	resp.Body["model"] = "claude-sonnet"
	resp.Body["usage"] = map[string]any{
		"input_tokens":  float64(200),
		"output_tokens": float64(100),
	}

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, receivedEvent)
	data, ok := receivedEvent["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(200), data["prompt_tokens"])
	assert.Equal(t, float64(100), data["completion_tokens"])
	assert.Equal(t, float64(300), data["total_tokens"])
}

func TestProcessResponse_OpenAICachedTokens(t *testing.T) {
	var mu sync.Mutex
	var receivedEvent map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/events" {
			var evt map[string]any
			_ = json.NewDecoder(r.Body).Decode(&evt)
			mu.Lock()
			receivedEvent = evt
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newTestPlugin(t, srv.URL, true)
	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringModelKey, "gpt-5.5")

	resp := requesthandling.NewInferenceResponse()
	resp.Body["model"] = "gpt-5.5"
	resp.Body["usage"] = map[string]any{
		"prompt_tokens":     float64(5000),
		"completion_tokens": float64(200),
		"total_tokens":      float64(5200),
		"prompt_tokens_details": map[string]any{
			"cached_tokens": float64(4500),
		},
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": float64(50),
		},
	}

	err := p.ProcessResponse(context.Background(), cs, resp)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, receivedEvent)
	data, ok := receivedEvent["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(4500), data["cached_input_tokens"])
	assert.Equal(t, float64(50), data["reasoning_tokens"])
}

func TestExtractTokenCounts(t *testing.T) {
	tests := []struct {
		name       string
		usage      map[string]any
		wantPrompt int
		wantCompl  int
		wantTotal  int
	}{
		{
			name:       "OpenAI format",
			usage:      map[string]any{"prompt_tokens": float64(10), "completion_tokens": float64(20), "total_tokens": float64(30)},
			wantPrompt: 10, wantCompl: 20, wantTotal: 30,
		},
		{
			name:       "Anthropic format",
			usage:      map[string]any{"input_tokens": float64(50), "output_tokens": float64(25)},
			wantPrompt: 50, wantCompl: 25, wantTotal: 75,
		},
		{
			name:       "OpenAI without total",
			usage:      map[string]any{"prompt_tokens": float64(10), "completion_tokens": float64(20)},
			wantPrompt: 10, wantCompl: 20, wantTotal: 30,
		},
		{
			name:       "empty usage",
			usage:      map[string]any{},
			wantPrompt: 0, wantCompl: 0, wantTotal: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, c, total := extractTokenCounts(tt.usage)
			assert.Equal(t, tt.wantPrompt, p)
			assert.Equal(t, tt.wantCompl, c)
			assert.Equal(t, tt.wantTotal, total)
		})
	}
}

// --- Helpers ---

func newTestPlugin(t *testing.T, meteringURL string, failOpen bool) *ExternalMeteringPlugin {
	t.Helper()
	config, _ := json.Marshal(map[string]any{
		"meteringURL":    meteringURL,
		"timeoutSeconds": 2,
		"failOpen":       failOpen,
	})
	bp, err := ExternalMeteringFactory("test", config, nil)
	require.NoError(t, err)
	return bp.(*ExternalMeteringPlugin)
}

func newTestRequest(username, group, subscription, model string, streaming bool) *requesthandling.InferenceRequest {
	req := requesthandling.NewInferenceRequest()
	if username != "" {
		req.Headers["x-maas-username"] = username
	}
	if group != "" {
		req.Headers["x-maas-group"] = group
	}
	if subscription != "" {
		req.Headers["x-maas-subscription"] = subscription
	}
	req.Body["model"] = model
	if streaming {
		req.Body["stream"] = true
	}
	return req
}

func newTestResponse(prompt, completion, total int) *requesthandling.InferenceResponse {
	resp := requesthandling.NewInferenceResponse()
	resp.Body["model"] = "gpt-4o"
	resp.Body["usage"] = map[string]any{
		"prompt_tokens":     float64(prompt),
		"completion_tokens": float64(completion),
		"total_tokens":      float64(total),
	}
	return resp
}

// --- splitSSELines Tests ---

func TestSplitSSELines(t *testing.T) {
	tests := []struct {
		name  string
		chunk string
		want  []string
	}{
		{"data prefix", "data: {\"type\":\"message\"}\n\n", []string{`{"type":"message"}`}},
		{"raw JSON", "{\"usage\":{\"input_tokens\":10}}", []string{`{"usage":{"input_tokens":10}}`}},
		{"data: [DONE]", "data: [DONE]\n\n", []string{"[DONE]"}},
		{"multi-line", "data: {\"a\":1}\n\ndata: {\"b\":2}\n\n", []string{`{"a":1}`, `{"b":2}`}},
		{"empty lines ignored", "\n\ndata: {\"c\":3}\n\n\n", []string{`{"c":3}`}},
		{"no data", "event: ping\n\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitSSELines(tt.chunk)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- extractUsageFromChunk Tests ---

func TestExtractUsageFromChunk(t *testing.T) {
	tests := []struct {
		name    string
		chunk   string
		wantKey string // key to check in returned usage map, "" means expect nil
	}{
		{
			"OpenAI usage in top-level",
			`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			"prompt_tokens",
		},
		{
			"Anthropic usage in message",
			`data: {"type":"message_delta","delta":{},"usage":{"input_tokens":100,"output_tokens":50}}`,
			"input_tokens",
		},
		{
			"Anthropic message.usage",
			`{"message":{"usage":{"input_tokens":200}}}`,
			"input_tokens",
		},
		{
			"OpenAI Responses API response.usage",
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":300,"output_tokens":100}}}`,
			"input_tokens",
		},
		{
			"delta.usage",
			`data: {"delta":{"usage":{"prompt_tokens":50}}}`,
			"prompt_tokens",
		},
		{
			"no usage",
			`data: {"type":"content_block_delta","delta":{"text":"hello"}}`,
			"",
		},
		{
			"data: [DONE]",
			"data: [DONE]\n\n",
			"",
		},
		{
			"empty chunk",
			"",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUsageFromChunk(tt.chunk)
			if tt.wantKey == "" {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got, "expected usage map")
				assert.Contains(t, got, tt.wantKey)
			}
		})
	}
}

// --- ProcessResponseChunk Tests ---

func TestProcessResponseChunk_UsageInFinalChunk(t *testing.T) {
	var reported sync.WaitGroup
	reported.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			reported.Done()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	raw := json.RawMessage(`{"meteringURL":"` + srv.URL + `","failOpen":true}`)
	p, err := ExternalMeteringStreamingFactory("metering", raw, nil)
	require.NoError(t, err)
	sp := p.(*ExternalMeteringStreamingPlugin)

	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringGroupKey, "ai-eng")
	cs.Write(state.MeteringSubscriptionKey, "ai-eng")
	cs.Write(state.MeteringModelKey, "claude-opus-4-8")
	resp := requesthandling.NewInferenceResponse()

	// Non-final chunk with no usage
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, `data: {"type":"content_block_delta","delta":{"text":"hi"}}`, false)
	assert.NoError(t, err)

	// Final chunk with usage
	err = sp.ProcessResponseChunk(context.Background(), cs, resp,
		`data: {"type":"message_delta","usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150}}`, true)
	assert.NoError(t, err)

	reported.Wait()
}

// TestProcessResponseChunk_UsageInSplitCompletedEvent reproduces the OpenAI
// Responses API (Codex) case: usage lives in a large response.completed SSE
// event that Envoy splits across chunk boundaries, so no individual chunk parses
// as JSON. The reassembled buffer must recover the usage on the final chunk.
func TestProcessResponseChunk_UsageInSplitCompletedEvent(t *testing.T) {
	var reported sync.WaitGroup
	reported.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			reported.Done()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	raw := json.RawMessage(`{"meteringURL":"` + srv.URL + `","failOpen":true}`)
	p, err := ExternalMeteringStreamingFactory("metering", raw, nil)
	require.NoError(t, err)
	sp := p.(*ExternalMeteringStreamingPlugin)

	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringGroupKey, "ai-eng")
	cs.Write(state.MeteringSubscriptionKey, "ai-eng")
	cs.Write(state.MeteringModelKey, "gpt-5.5")
	resp := requesthandling.NewInferenceResponse()

	completed := `event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"gpt-5.5","usage":{"input_tokens":8,"output_tokens":6,"total_tokens":14}}}`
	mid := len(completed) / 2

	// Neither half parses as JSON on its own.
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, completed[:mid], false)
	assert.NoError(t, err)
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, completed[mid:], false)
	assert.NoError(t, err)
	// Final (empty) chunk triggers reassembly and reporting.
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, "", true)
	assert.NoError(t, err)

	reported.Wait()
}

// TestProcessResponseChunk_UsageInPrettyPrintedJSON reproduces the non-streaming
// OpenAI Responses API case: the body is pretty-printed multi-line JSON split
// across chunks, so no single line is valid JSON — only the reassembled buffer
// parsed as a whole document yields the usage.
func TestProcessResponseChunk_UsageInPrettyPrintedJSON(t *testing.T) {
	var reported sync.WaitGroup
	reported.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			reported.Done()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	raw := json.RawMessage(`{"meteringURL":"` + srv.URL + `","failOpen":true}`)
	p, err := ExternalMeteringStreamingFactory("metering", raw, nil)
	require.NoError(t, err)
	sp := p.(*ExternalMeteringStreamingPlugin)

	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringGroupKey, "ai-eng")
	cs.Write(state.MeteringSubscriptionKey, "ai-eng")
	cs.Write(state.MeteringModelKey, "gpt-5.5")
	resp := requesthandling.NewInferenceResponse()

	body := `{
  "id": "resp_1",
  "object": "response",
  "status": "completed",
  "model": "gpt-5.5",
  "usage": {
    "input_tokens": 8,
    "output_tokens": 6,
    "total_tokens": 14
  }
}`
	mid := len(body) / 2

	err = sp.ProcessResponseChunk(context.Background(), cs, resp, body[:mid], false)
	assert.NoError(t, err)
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, body[mid:], false)
	assert.NoError(t, err)
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, "", true)
	assert.NoError(t, err)

	reported.Wait()
}

func TestProcessResponseChunk_NoUsageData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	raw := json.RawMessage(`{"meteringURL":"` + srv.URL + `","failOpen":true}`)
	p, err := ExternalMeteringStreamingFactory("metering", raw, nil)
	require.NoError(t, err)
	sp := p.(*ExternalMeteringStreamingPlugin)

	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	resp := requesthandling.NewInferenceResponse()

	// Final chunk with no usage — should not error
	err = sp.ProcessResponseChunk(context.Background(), cs, resp, "", true)
	assert.NoError(t, err)
}

func TestProcessResponseChunk_MissingUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	raw := json.RawMessage(`{"meteringURL":"` + srv.URL + `","failOpen":true}`)
	p, err := ExternalMeteringStreamingFactory("metering", raw, nil)
	require.NoError(t, err)
	sp := p.(*ExternalMeteringStreamingPlugin)

	cs := plugin.NewCycleState()
	resp := requesthandling.NewInferenceResponse()

	// No username in CycleState — should skip
	err = sp.ProcessResponseChunk(context.Background(), cs, resp,
		`data: {"usage":{"input_tokens":10,"output_tokens":5}}`, true)
	assert.NoError(t, err)
}

// TestProcessResponseChunk_ErrorResponseReported verifies that a provider error
// body (no usage anywhere in the stream) is reported as an
// inference.request.error event instead of being silently dropped.
func TestProcessResponseChunk_ErrorResponseReported(t *testing.T) {
	var received map[string]any
	var reported sync.WaitGroup
	reported.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &received)
			reported.Done()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	raw := json.RawMessage(`{"meteringURL":"` + srv.URL + `","failOpen":true}`)
	p, err := ExternalMeteringStreamingFactory("metering", raw, nil)
	require.NoError(t, err)
	sp := p.(*ExternalMeteringStreamingPlugin)

	cs := plugin.NewCycleState()
	cs.Write(state.MeteringUsernameKey, "alice")
	cs.Write(state.MeteringGroupKey, "ai-eng")
	cs.Write(state.MeteringSubscriptionKey, "ai-eng")
	cs.Write(state.MeteringModelKey, "claude-sonnet-4-6")
	resp := requesthandling.NewInferenceResponse()
	resp.Headers[":status"] = "400"

	errBody := `{"type":"error","error":{"type":"invalid_request_error","message":"context_management: Extra inputs are not permitted"}}`
	mid := len(errBody) / 2

	require.NoError(t, sp.ProcessResponseChunk(context.Background(), cs, resp, errBody[:mid], false))
	require.NoError(t, sp.ProcessResponseChunk(context.Background(), cs, resp, errBody[mid:], false))
	require.NoError(t, sp.ProcessResponseChunk(context.Background(), cs, resp, "", true))

	reported.Wait()
	assert.Equal(t, "inference.request.error", received["type"])
	data, ok := received["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "alice", data["user"])
	assert.Equal(t, float64(400), data["status_code"])
	assert.Equal(t, "invalid_request_error", data["error_type"])
}

// TestExtractErrorFromResponse_TruncatesLongMessage verifies provider error text
// is capped before being persisted to metering.
func TestExtractErrorFromResponse_TruncatesLongMessage(t *testing.T) {
	long := strings.Repeat("x", 2*maxErrorMessageLen)
	buf := `{"type":"error","error":{"type":"invalid_request_error","message":"` + long + `"}}`

	resp := requesthandling.NewInferenceResponse()
	resp.Headers[":status"] = "400"

	info := extractErrorFromResponse(buf, resp)
	require.NotNil(t, info)
	msg, ok := info["error_message"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, len(msg), maxErrorMessageLen+len("…"))
	assert.Equal(t, 400, info["status_code"])
}

// TestExtractErrorFromResponse_MalformedBufferNotMisparsed verifies that a buffer
// that fails full-body and per-line JSON parsing yields nil rather than acting on
// a partially-filled map.
func TestExtractErrorFromResponse_MalformedBufferNotMisparsed(t *testing.T) {
	// Valid prefix that would partially fill the map before the syntax error.
	buf := `{"error":{"type":"invalid_request_error","message":"boom"},INVALID`

	info := extractErrorFromResponse(buf, nil)
	assert.Nil(t, info, "partially-parsed garbage must not produce an error event")
}

// TestExtractErrorFromResponse_NonNumericStatus verifies a garbage :status header
// degrades to status_code 0 rather than failing.
func TestExtractErrorFromResponse_NonNumericStatus(t *testing.T) {
	resp := requesthandling.NewInferenceResponse()
	resp.Headers[":status"] = "abc"

	info := extractErrorFromResponse(`{"error":{"type":"x","message":"y"}}`, resp)
	require.NotNil(t, info)
	assert.Equal(t, 0, info["status_code"])
}
