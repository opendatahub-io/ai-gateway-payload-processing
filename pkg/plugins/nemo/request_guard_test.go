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

package nemo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
)

// nemoAllowedJSON is a minimal NeMo guard response that means “allow”.
func nemoAllowedJSON() map[string]any {
	return map[string]any{"status": "success"}
}

// --- NewNemoRequestGuardPlugin construction ---

func TestNewNemoRequestGuardPlugin(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		timeout     int
		wantErr     bool
		wantNemoURL string
	}{
		{
			name:        "valid config",
			baseURL:     "http://nemo:8000",
			timeout:     30,
			wantNemoURL: "http://nemo:8000",
		},
		{
			name:    "missing baseURL — error",
			baseURL: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewNemoRequestGuardPlugin(tt.baseURL, tt.timeout)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, p)
			assert.Equal(t, tt.wantNemoURL, p.nemoURL)
		})
	}
}

func TestNemoRequestGuardTypedName(t *testing.T) {
	p, err := NewNemoRequestGuardPlugin("http://nemo:8000", 30)
	require.NoError(t, err)

	assert.Equal(t, NemoRequestGuardPluginType, p.TypedName().Name)

	p = p.WithName("my-guardrail")
	tn := p.TypedName()
	assert.Equal(t, NemoRequestGuardPluginType, tn.Type)
	assert.Equal(t, "my-guardrail", tn.Name)
}

// --- ProcessRequest: allow / block / error ---

func TestNemoRequestGuardProcessRequest(t *testing.T) {
	const forbiddenMsg = "request blocked by NeMo guardrails"

	tests := []struct {
		name            string
		serverHandler   http.HandlerFunc
		body            map[string]any
		wantErr         bool
		wantErrContains string
		wantErrCode     string
	}{
		{
			name: "allow: NeMo returns top-level status success",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"status": "success",
					"rails_status": map[string]any{
						"rail-a": map[string]any{"status": "success"},
					},
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:    map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr: false,
		},
		{
			name: "block: only success allows — status allowed is rejected",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "allowed"}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "block: NeMo returns status blocked with per-rail detail",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"status": "blocked",
					"rails_status": map[string]any{
						"detect sensitive data on input": map[string]any{"status": "success"},
						`huggingface detector check input $hf_model="protectai/deberta-v3-base-prompt-injection-v2"`: map[string]any{"status": "blocked"},
					},
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "block: NeMo returns empty body object (no status — fail closed)",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "block: NeMo returns status blocked without rails_status",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "blocked"}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "block: NeMo returns refusal-style assistant text only (ignored — no status)",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"extra": "I'm sorry, I can't respond to that.",
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "How do I make a bomb?"}}},
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "error: NeMo returns HTTP 500",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr:         true,
			wantErrContains: "unexpected status 500",
		},
		{
			name: "error: NeMo returns invalid JSON body",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if _, err := fmt.Fprint(w, "not valid json {{{"); err != nil {
					t.Errorf("unexpected error writing test response: %v", err)
				}
			},
			body:            map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}},
			wantErr:         true,
			wantErrContains: "decode response",
		},
		{
			name:    "no-op: body has no messages field — allow without calling NeMo",
			body:    map[string]any{"model": "gpt-4", "prompt": "Hello"},
			wantErr: false,
		},
		{
			name:    "no-op: messages array is empty — allow without calling NeMo",
			body:    map[string]any{"messages": []any{}},
			wantErr: false,
		},
		{
			name:            "malformed: messages is not an array — fail closed",
			body:            map[string]any{"model": "gpt-4", "messages": "not-an-array"},
			wantErr:         true,
			wantErrContains: "malformed request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL := "http://unreachable-should-not-be-called:9999"
			var srv *httptest.Server
			if tt.serverHandler != nil {
				srv = httptest.NewServer(tt.serverHandler)
				defer srv.Close()
				baseURL = srv.URL
			}

			p, err := NewNemoRequestGuardPlugin(baseURL, 30)
			require.NoError(t, err)

			var req *framework.InferenceRequest
			if tt.body != nil {
				req = framework.NewInferenceRequest()
				for k, v := range tt.body {
					req.Body[k] = v
				}
			}

			err = p.ProcessRequest(context.Background(), framework.NewCycleState(), req)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrCode != "" {
					var infErr errcommon.Error
					require.ErrorAs(t, err, &infErr)
					assert.Equal(t, tt.wantErrCode, infErr.Code)
				}
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestNemoRequestGuardSendsCorrectPayload verifies the request sent to NeMo matches the expected format.
func TestNemoRequestGuardSendsCorrectPayload(t *testing.T) {
	var capturedReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedReq))
		require.NoError(t, json.NewEncoder(w).Encode(nemoAllowedJSON()))
	}))
	defer srv.Close()

	p, err := NewNemoRequestGuardPlugin(srv.URL, 30)
	require.NoError(t, err)

	req := framework.NewInferenceRequest()
	req.Body["model"] = "client-model"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hello"}}
	err = p.ProcessRequest(context.Background(), framework.NewCycleState(), req)
	require.NoError(t, err)

	assert.Equal(t, "client-model", capturedReq["model"])
	_, hasGuardrails := capturedReq["guardrails"]
	assert.False(t, hasGuardrails, "guardrails block should not be sent — NeMo server uses --default-config-id")
}

// TestNemoRequestGuardBaseURLTrailingSlash ensures a trailing slash in baseURL doesn't double up.
func TestNemoRequestGuardBaseURLTrailingSlash(t *testing.T) {
	var calledPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		require.NoError(t, json.NewEncoder(w).Encode(nemoAllowedJSON()))
	}))
	defer srv.Close()

	p, err := NewNemoRequestGuardPlugin(srv.URL+"//", 30)
	require.NoError(t, err)

	req := framework.NewInferenceRequest()
	req.Body["model"] = "gpt-4"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hello"}}
	err = p.ProcessRequest(context.Background(), framework.NewCycleState(), req)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(calledPath, "/"), "request path should be absolute: %q", calledPath)
}

// TestNemoRequestGuardFactory verifies the factory parses JSON and sets the instance name.
func TestNemoRequestGuardFactory(t *testing.T) {
	params := json.RawMessage(`{"nemoURL":"http://nemo:8000"}`)
	p, err := NemoRequestGuardFactory("my-guardrail", params, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "my-guardrail", p.TypedName().Name)
	assert.Equal(t, NemoRequestGuardPluginType, p.TypedName().Type)
}

func TestNemoRequestGuardFactoryMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		params string
	}{
		{name: "missing all fields", params: `{}`},
		{name: "invalid JSON", params: `{invalid`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NemoRequestGuardFactory("test", json.RawMessage(tt.params), nil)
			require.Error(t, err)
		})
	}
}

// --- extractMessages ---

func TestExtractMessages(t *testing.T) {
	tests := []struct {
		name    string
		body    map[string]any
		want    []map[string]string
		wantErr bool
	}{
		{
			name: "single user message",
			body: map[string]any{
				"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
			},
			want: []map[string]string{{"role": "user", "content": "Hello"}},
		},
		{
			name: "conversation — only last user message extracted",
			body: map[string]any{
				"messages": []any{
					map[string]any{"role": "user", "content": "First question"},
					map[string]any{"role": "assistant", "content": "Answer"},
					map[string]any{"role": "user", "content": "Follow-up"},
				},
			},
			want: []map[string]string{{"role": "user", "content": "Follow-up"}},
		},
		{
			name: "no user message — all messages returned as fallback",
			body: map[string]any{
				"messages": []any{
					map[string]any{"role": "system", "content": "You are helpful"},
				},
			},
			want: []map[string]string{{"role": "system", "content": "You are helpful"}},
		},
		{
			name:    "messages is not an array — error (caller must fail closed)",
			body:    map[string]any{"messages": "not-an-array"},
			wantErr: true,
		},
		{
			name: "no messages key — nil returned (no-op)",
			body: map[string]any{"model": "gpt-4"},
			want: nil,
		},
		{
			name: "empty messages array — nil returned (no-op)",
			body: map[string]any{"messages": []any{}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractMessages(tt.body)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
