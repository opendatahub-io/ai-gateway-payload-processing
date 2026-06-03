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
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
)

// --- NewNemoResponseGuardPlugin construction ---

func TestNewNemoResponseGuardPlugin(t *testing.T) {
	tests := []struct {
		name        string
		nemoURL     string
		timeout     int
		wantErr     bool
		wantNemoURL string
	}{
		{
			name:        "valid config",
			nemoURL:     "http://nemo:8000/v1/guardrail/checks",
			timeout:     30,
			wantNemoURL: "http://nemo:8000/v1/guardrail/checks",
		},
		{
			name:    "missing nemoURL — error",
			nemoURL: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewNemoResponseGuardPlugin(tt.nemoURL, tt.timeout)
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

func TestNemoResponseGuardTypedName(t *testing.T) {
	p, err := NewNemoResponseGuardPlugin("http://nemo:8000/v1/guardrail/checks", 30)
	require.NoError(t, err)

	assert.Equal(t, NemoResponseGuardPluginType, p.TypedName().Name)

	p.WithName("my-output-guard")
	tn := p.TypedName()
	assert.Equal(t, NemoResponseGuardPluginType, tn.Type)
	assert.Equal(t, "my-output-guard", tn.Name)
}

// --- ProcessResponse: allow / block / error ---

func TestNemoResponseGuardProcessResponse(t *testing.T) {
	const forbiddenMsg = "response blocked by NeMo guardrails"

	validResponseBody := func(content string) map[string]any {
		return map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": content,
					},
				},
			},
		}
	}

	tests := []struct {
		name            string
		serverHandler   http.HandlerFunc
		body            map[string]any
		wantErr         bool
		wantErrContains string
		wantErrCode     string
	}{
		{
			name: "allow: NeMo returns status passed",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"status": "passed",
					"rails_status": map[string]any{
						"output-rail": map[string]any{"status": "passed"},
					},
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:    validResponseBody("The weather is sunny today."),
			wantErr: false,
		},
		{
			name: "redact: NeMo returns modified with redacted content",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(nemoModifiedJSON("The user's email is <EMAIL_ADDRESS>")); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:    validResponseBody("The user's email is test@example.com"),
			wantErr: false,
		},
		{
			name: "fail-closed: NeMo returns modified but no redacted content in guardrails_data",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"status": "modified",
					"rails_status": map[string]any{
						"mask sensitive data on output": map[string]any{"status": "modified"},
					},
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            validResponseBody("The user's email is test@example.com"),
			wantErr:         true,
			wantErrContains: "no redacted content found",
			wantErrCode:     errcommon.Internal,
		},
		{
			name: "fail-closed: unknown status — status success is no longer recognized",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "success"}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            validResponseBody("Hello"),
			wantErr:         true,
			wantErrContains: "unknown NeMo guardrails status",
			wantErrCode:     errcommon.Internal,
		},
		{
			name: "block: NeMo returns status blocked with per-rail detail",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"status": "blocked",
					"rails_status": map[string]any{
						`huggingface detector check output $hf_model="ibm-granite/granite-guardian-hap-38m"`: map[string]any{"status": "blocked"},
					},
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            validResponseBody("this message should be blocked."),
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "fail-closed: NeMo returns empty body (no status)",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            validResponseBody("Some content"),
			wantErr:         true,
			wantErrContains: "unknown NeMo guardrails status",
			wantErrCode:     errcommon.Internal,
		},
		{
			name: "block: NeMo returns status blocked without rails_status",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "blocked"}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            validResponseBody("Toxic content here"),
			wantErr:         true,
			wantErrContains: forbiddenMsg,
			wantErrCode:     errcommon.Forbidden,
		},
		{
			name: "fail-closed: NeMo returns refusal-style text only (no status)",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{
					"extra": "I'm sorry, I can't respond to that.",
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			},
			body:            validResponseBody("Some toxic output"),
			wantErr:         true,
			wantErrContains: "unknown NeMo guardrails status",
			wantErrCode:     errcommon.Internal,
		},
		{
			name: "error: NeMo returns HTTP 500",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			body:            validResponseBody("Hello"),
			wantErr:         true,
			wantErrContains: "unexpected status 500",
		},
		{
			name: "error: NeMo returns invalid JSON",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if _, err := fmt.Fprint(w, "not valid json {{{"); err != nil {
					t.Errorf("unexpected error writing test response: %v", err)
				}
			},
			body:            validResponseBody("Hello"),
			wantErr:         true,
			wantErrContains: "decode nemo response",
		},
		{
			name:    "no-op: no choices in response — allow without calling NeMo",
			body:    map[string]any{"object": "chat.completion"},
			wantErr: false,
		},
		{
			name:    "no-op: empty choices array — allow without calling NeMo",
			body:    map[string]any{"choices": []any{}},
			wantErr: false,
		},
		{
			name: "no-op: assistant content is empty — allow without calling NeMo",
			body: map[string]any{
				"choices": []any{
					map[string]any{
						"message": map[string]any{"role": "assistant", "content": ""},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "no-op: tool-call response with null content — allow without calling NeMo",
			body: map[string]any{
				"choices": []any{
					map[string]any{
						"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "fail closed: choices has no message or delta — unsupported shape",
			body: map[string]any{
				"choices": []any{
					map[string]any{"index": 0, "finish_reason": "stop"},
				},
			},
			wantErr:         true,
			wantErrContains: "no message or delta field",
			wantErrCode:     errcommon.Internal,
		},
		{
			name: "fail closed: content is not a string — unsupported shape",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"content": 42}},
				},
			},
			wantErr:         true,
			wantErrContains: "content is not a string",
			wantErrCode:     errcommon.Internal,
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

			p, err := NewNemoResponseGuardPlugin(baseURL, 30)
			require.NoError(t, err)

			resp := requesthandling.NewInferenceResponse()
			for k, v := range tt.body {
				resp.Body[k] = v
			}

			err = p.ProcessResponse(context.Background(), plugin.NewCycleState(), resp)

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

// TestNemoResponseGuardSendsCorrectPayload verifies the request sent to NeMo matches the expected format.
func TestNemoResponseGuardSendsCorrectPayload(t *testing.T) {
	var capturedReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedReq))
		require.NoError(t, json.NewEncoder(w).Encode(nemoAllowedJSON()))
	}))
	defer srv.Close()

	p, err := NewNemoResponseGuardPlugin(srv.URL, 30)
	require.NoError(t, err)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["choices"] = []any{
		map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Here is the answer."},
		},
	}
	err = p.ProcessResponse(context.Background(), plugin.NewCycleState(), resp)
	require.NoError(t, err)

	messages, ok := capturedReq["messages"].([]any)
	require.True(t, ok, "messages should be an array")
	require.Len(t, messages, 1)
	msg := messages[0].(map[string]any)
	assert.Equal(t, "assistant", msg["role"])
	assert.Equal(t, "Here is the answer.", msg["content"])
}

// TestNemoResponseGuardForwardsModel verifies the model field from the response body is forwarded to NeMo.
func TestNemoResponseGuardForwardsModel(t *testing.T) {
	var capturedReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedReq))
		require.NoError(t, json.NewEncoder(w).Encode(nemoAllowedJSON()))
	}))
	defer srv.Close()

	p, err := NewNemoResponseGuardPlugin(srv.URL, 30)
	require.NoError(t, err)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["model"] = "gpt-4"
	resp.Body["choices"] = []any{
		map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Answer"},
		},
	}
	err = p.ProcessResponse(context.Background(), plugin.NewCycleState(), resp)
	require.NoError(t, err)

	assert.Equal(t, "gpt-4", capturedReq["model"])
}

// TestNemoResponseGuardBaseURLTrailingSlash ensures a trailing slash in nemoURL doesn't double up.
func TestNemoResponseGuardBaseURLTrailingSlash(t *testing.T) {
	var calledPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		require.NoError(t, json.NewEncoder(w).Encode(nemoAllowedJSON()))
	}))
	defer srv.Close()

	p, err := NewNemoResponseGuardPlugin(srv.URL+"//", 30)
	require.NoError(t, err)

	resp := requesthandling.NewInferenceResponse()
	resp.Body["choices"] = []any{
		map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Hello"},
		},
	}
	err = p.ProcessResponse(context.Background(), plugin.NewCycleState(), resp)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(calledPath, "/"), "request path should be absolute: %q", calledPath)
}

// TestNemoResponseGuardFactory verifies the factory parses JSON and sets the instance name.
func TestNemoResponseGuardFactory(t *testing.T) {
	params := json.RawMessage(`{"nemoURL":"http://nemo:8000/v1/guardrail/checks"}`)
	p, err := NemoResponseGuardFactory("my-output-guard", params, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "my-output-guard", p.TypedName().Name)
	assert.Equal(t, NemoResponseGuardPluginType, p.TypedName().Type)
}

func TestNemoResponseGuardFactoryMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		params string
	}{
		{name: "missing all fields", params: `{}`},
		{name: "invalid JSON", params: `{invalid`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NemoResponseGuardFactory("test", json.RawMessage(tt.params), nil)
			require.Error(t, err)
		})
	}
}

// --- extractAssistantMessages ---

func TestExtractAssistantMessages(t *testing.T) {
	tests := []struct {
		name    string
		body    map[string]any
		want    []map[string]string
		wantErr bool
	}{
		{
			name: "single choice with message",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"role": "assistant", "content": "Hello"}},
				},
			},
			want: []map[string]string{{"role": "assistant", "content": "Hello"}},
		},
		{
			name: "multiple choices",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"role": "assistant", "content": "First"}},
					map[string]any{"message": map[string]any{"role": "assistant", "content": "Second"}},
				},
			},
			want: []map[string]string{
				{"role": "assistant", "content": "First"},
				{"role": "assistant", "content": "Second"},
			},
		},
		{
			name: "delta field (streaming)",
			body: map[string]any{
				"choices": []any{
					map[string]any{"delta": map[string]any{"role": "assistant", "content": "Streaming chunk"}},
				},
			},
			want: []map[string]string{{"role": "assistant", "content": "Streaming chunk"}},
		},
		{
			name: "no choices key — nil (not a chat completion)",
			body: map[string]any{"object": "chat.completion"},
			want: nil,
		},
		{
			name: "empty choices — nil",
			body: map[string]any{"choices": []any{}},
			want: nil,
		},
		{
			name: "empty content — skipped",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"role": "assistant", "content": ""}},
				},
			},
			want: nil,
		},
		{
			name: "null content — skipped (tool-call response)",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{}}},
				},
			},
			want: nil,
		},
		{
			name: "missing content key — skipped",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"role": "assistant"}},
				},
			},
			want: nil,
		},
		{
			name: "no message or delta — fail closed",
			body: map[string]any{
				"choices": []any{
					map[string]any{"index": 0},
				},
			},
			wantErr: true,
		},
		{
			name: "content is not a string — fail closed",
			body: map[string]any{
				"choices": []any{
					map[string]any{"message": map[string]any{"content": 42}},
				},
			},
			wantErr: true,
		},
		{
			name:    "choices is not an array — fail closed",
			body:    map[string]any{"choices": "not-an-array"},
			wantErr: true,
		},
		{
			name: "mcp text content",
			body: map[string]any{
				"jsonrpc": "2.0",
				"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": "Hello"}}},
			},
			want: []map[string]string{{"role": "assistant", "content": "Hello"}},
		},
		{
			name: "mcp text content — empty content array — nil",
			body: map[string]any{
				"jsonrpc": "2.0",
				"result":  map[string]any{"content": []any{}},
			},
			want: nil,
		},
		{
			name: "mcp text content — empty content entry — nil",
			body: map[string]any{
				"jsonrpc": "2.0",
				"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}}},
			},
			want: nil,
		},
		{
			name: "mcp text content — text is not a string — fail closed",
			body: map[string]any{
				"jsonrpc": "2.0",
				"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": 42}}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractAssistantMessages(tt.body)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Redaction ---

func TestNemoResponseGuardRedaction(t *testing.T) {
	tests := []struct {
		name            string
		nemoResp        map[string]any
		body            map[string]any
		wantErr         bool
		wantErrContains string
		wantContents    []string // expected content for each choice after redaction
	}{
		{
			name:     "redacted content replaces assistant message",
			nemoResp: nemoModifiedJSON("The user's email is <EMAIL_ADDRESS>"),
			body: map[string]any{
				"choices": []any{
					map[string]any{
						"message": map[string]any{"role": "assistant", "content": "The user's email is test@example.com"},
					},
				},
			},
			wantContents: []string{"The user's email is <EMAIL_ADDRESS>"},
		},
		{
			name:     "redacted content replaces all choices in multi-choice response",
			nemoResp: nemoModifiedJSON("First choice", "Name is <PERSON>"),
			body: map[string]any{
				"choices": []any{
					map[string]any{
						"message": map[string]any{"role": "assistant", "content": "First choice"},
					},
					map[string]any{
						"message": map[string]any{"role": "assistant", "content": "Name is Rob"},
					},
				},
			},
			wantContents: []string{"First choice", "Name is <PERSON>"},
		},
		{
			name:     "redacted content replaces streaming delta",
			nemoResp: nemoModifiedJSON("SSN is <SSN>"),
			body: map[string]any{
				"choices": []any{
					map[string]any{
						"delta": map[string]any{"role": "assistant", "content": "SSN is 123-45-6789"},
					},
				},
			},
			wantContents: []string{"SSN is <SSN>"},
		},
		{
			name:     "model field preserved after redaction",
			nemoResp: nemoModifiedJSON("redacted"),
			body: map[string]any{
				"model": "gpt-4",
				"choices": []any{
					map[string]any{
						"message": map[string]any{"role": "assistant", "content": "original"},
					},
				},
			},
			wantContents: []string{"redacted"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(tt.nemoResp); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			}))
			defer srv.Close()

			p, err := NewNemoResponseGuardPlugin(srv.URL, 30)
			require.NoError(t, err)

			resp := requesthandling.NewInferenceResponse()
			for k, v := range tt.body {
				resp.Body[k] = v
			}

			err = p.ProcessResponse(context.Background(), plugin.NewCycleState(), resp)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}
			require.NoError(t, err)

			if len(tt.wantContents) > 0 {
				choices := resp.Body["choices"].([]any)
				require.Len(t, choices, len(tt.wantContents))
				for i, want := range tt.wantContents {
					choice := choices[i].(map[string]any)
					var content string
					if msg, ok := choice["message"].(map[string]any); ok {
						content = msg["content"].(string)
					} else if delta, ok := choice["delta"].(map[string]any); ok {
						content = delta["content"].(string)
					}
					assert.Equal(t, want, content, "choice[%d] content mismatch", i)
				}
			}

			if model, ok := tt.body["model"]; ok {
				assert.Equal(t, model, resp.Body["model"])
			}
		})
	}
}
