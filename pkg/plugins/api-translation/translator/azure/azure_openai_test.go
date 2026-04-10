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

package azure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslateRequest_BodyPassthrough(t *testing.T) {
	body := map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are helpful."},
			map[string]any{"role": "user", "content": "Hello"},
		},
		"temperature":       0.7,
		"top_p":             0.9,
		"max_tokens":        float64(1000),
		"stream":            true,
		"stop":              []any{"END"},
		"n":                 float64(1),
		"presence_penalty":  0.5,
		"frequency_penalty": 0.3,
	}

	translatedBody, headers, headersToRemove, err := NewAzureOpenAITranslator().TranslateRequest(body)
	require.NoError(t, err)

	assert.Nil(t, translatedBody, "body should not be mutated for Azure OpenAI")

	expectedPath := fmt.Sprintf("/openai/deployments/gpt-4o/chat/completions?api-version=%s", defaultAPIVersion)
	assert.Equal(t, expectedPath, headers[":path"])
	assert.Equal(t, "application/json", headers["content-type"])
	assert.Len(t, headers, 2)
	assert.Nil(t, headersToRemove)
}

func TestTranslateRequest_ModelUsedAsDeploymentID(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{"gpt-4o model", "gpt-4o"},
		{"gpt-4o-mini model", "gpt-4o-mini"},
		{"custom deployment name", "my-custom-deployment"},
		{"with dots", "gpt-4o.2025"},
		{"with underscore", "my_deployment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"model":    tt.model,
				"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
			}

			_, headers, _, err := NewAzureOpenAITranslator().TranslateRequest(body)
			require.NoError(t, err)

			expectedPath := fmt.Sprintf("/openai/deployments/%s/chat/completions?api-version=%s", tt.model, defaultAPIVersion)
			assert.Equal(t, expectedPath, headers[":path"])
		})
	}
}

func TestTranslateRequest_MissingModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := NewAzureOpenAITranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}

func TestTranslateRequest_EmptyModel(t *testing.T) {
	body := map[string]any{
		"model":    "",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := NewAzureOpenAITranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}

func TestTranslateRequest_InvalidModelCharacters(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{"query injection", "gpt-4o?api-version=hijacked&x="},
		{"path traversal", "../../../etc/passwd"},
		{"slash in model", "org/model-name"},
		{"space in model", "gpt 4o"},
		{"starts with hyphen", "-gpt-4o"},
		{"starts with dot", ".hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"model":    tt.model,
				"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
			}

			_, _, _, err := NewAzureOpenAITranslator().TranslateRequest(body)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid characters")
		})
	}
}

func TestTranslateResponse_CleanResponse(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion",
		"created": float64(1700000000),
		"model":   "gpt-4o",
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role":    "assistant",
					"content": "The answer is 4.",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "clean response without Azure-specific fields should not be mutated")
}

func TestTranslateResponse_StripsContentFilterResults(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion",
		"created": float64(1700000000),
		"model":   "gpt-4o",
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role":    "assistant",
					"content": "The answer is 4.",
				},
				"finish_reason": "stop",
				"content_filter_results": map[string]any{
					"hate":      map[string]any{"filtered": false, "severity": "safe"},
					"self_harm": map[string]any{"filtered": false, "severity": "safe"},
					"sexual":    map[string]any{"filtered": false, "severity": "safe"},
					"violence":  map[string]any{"filtered": false, "severity": "safe"},
				},
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, translatedBody)

	choices := translatedBody["choices"].([]any)
	choice := choices[0].(map[string]any)
	assert.NotContains(t, choice, "content_filter_results")

	assert.Equal(t, "chatcmpl-abc123", translatedBody["id"])
	assert.Equal(t, "chat.completion", translatedBody["object"])
	msg := choice["message"].(map[string]any)
	assert.Equal(t, "assistant", msg["role"])
	assert.Equal(t, "The answer is 4.", msg["content"])
	assert.Equal(t, "stop", choice["finish_reason"])
}

func TestTranslateResponse_StripsPromptFilterResults(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion",
		"created": float64(1700000000),
		"model":   "gpt-4o",
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello!",
				},
				"finish_reason": "stop",
			},
		},
		"prompt_filter_results": []any{
			map[string]any{
				"prompt_index": float64(0),
				"content_filter_results": map[string]any{
					"hate":      map[string]any{"filtered": false, "severity": "safe"},
					"self_harm": map[string]any{"filtered": false, "severity": "safe"},
				},
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(8),
			"completion_tokens": float64(2),
			"total_tokens":      float64(10),
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, translatedBody)

	assert.NotContains(t, translatedBody, "prompt_filter_results")
	assert.Equal(t, "chatcmpl-abc123", translatedBody["id"])
	assert.Contains(t, translatedBody, "usage")
}

func TestTranslateResponse_StripsBothAzureFields(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion",
		"created": float64(1700000000),
		"model":   "gpt-4o",
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role":    "assistant",
					"content": "The answer is 4.",
				},
				"finish_reason": "stop",
				"content_filter_results": map[string]any{
					"hate":      map[string]any{"filtered": false, "severity": "safe"},
					"self_harm": map[string]any{"filtered": false, "severity": "safe"},
					"sexual":    map[string]any{"filtered": false, "severity": "safe"},
					"violence":  map[string]any{"filtered": false, "severity": "safe"},
				},
			},
		},
		"prompt_filter_results": []any{
			map[string]any{
				"prompt_index": float64(0),
				"content_filter_results": map[string]any{
					"hate":      map[string]any{"filtered": false, "severity": "safe"},
					"self_harm": map[string]any{"filtered": false, "severity": "safe"},
					"sexual":    map[string]any{"filtered": false, "severity": "safe"},
					"violence":  map[string]any{"filtered": false, "severity": "safe"},
				},
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, translatedBody)

	assert.NotContains(t, translatedBody, "prompt_filter_results")

	choices := translatedBody["choices"].([]any)
	choice := choices[0].(map[string]any)
	assert.NotContains(t, choice, "content_filter_results")

	assert.Equal(t, "chatcmpl-abc123", translatedBody["id"])
	assert.Equal(t, "chat.completion", translatedBody["object"])
	assert.Equal(t, float64(1700000000), translatedBody["created"])
	assert.Equal(t, "gpt-4o", translatedBody["model"])
	assert.Contains(t, translatedBody, "usage")

	msg := choice["message"].(map[string]any)
	assert.Equal(t, "assistant", msg["role"])
	assert.Equal(t, "The answer is 4.", msg["content"])
	assert.Equal(t, "stop", choice["finish_reason"])
}

func TestTranslateResponse_EmptyBody(t *testing.T) {
	body := map[string]any{}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	assert.Nil(t, translatedBody)
}

func TestTranslateResponse_ErrorPassthrough(t *testing.T) {
	body := map[string]any{
		"error": map[string]any{
			"message": "The API deployment for this resource does not exist.",
			"type":    "invalid_request_error",
			"code":    "DeploymentNotFound",
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "Azure error responses are already in OpenAI format")
}

func TestTranslateResponse_StreamingChunkPassthrough(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion.chunk",
		"created": float64(1700000000),
		"model":   "gpt-4o",
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"delta": map[string]any{
					"content": "Hello",
				},
				"finish_reason": nil,
			},
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	assert.Nil(t, translatedBody)
}

func TestTranslateResponse_StreamingChunkStripsAzureFields(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion.chunk",
		"created": float64(1700000000),
		"model":   "gpt-4o",
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"delta": map[string]any{
					"content": "Hello",
				},
				"finish_reason": nil,
				"content_filter_results": map[string]any{
					"hate":      map[string]any{"filtered": false, "severity": "safe"},
					"self_harm": map[string]any{"filtered": false, "severity": "safe"},
				},
			},
		},
		"prompt_filter_results": []any{
			map[string]any{
				"prompt_index": float64(0),
				"content_filter_results": map[string]any{
					"hate": map[string]any{"filtered": false, "severity": "safe"},
				},
			},
		},
	}

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, translatedBody)

	assert.NotContains(t, translatedBody, "prompt_filter_results")
	choices := translatedBody["choices"].([]any)
	choice := choices[0].(map[string]any)
	assert.NotContains(t, choice, "content_filter_results")
	assert.Equal(t, "chat.completion.chunk", translatedBody["object"])
}

// TestTranslateResponse_LiveMockIntegration fetches a real Azure OpenAI response from
// the llm-katan mock server and verifies that TranslateResponse strips Azure-specific fields.
// Set LLM_KATAN_URL to enable (e.g. LLM_KATAN_URL=http://3.150.113.9:8000).
func TestTranslateResponse_LiveMockIntegration(t *testing.T) {
	baseURL := os.Getenv("LLM_KATAN_URL")
	if baseURL == "" {
		t.Skip("LLM_KATAN_URL not set, skipping live mock integration test")
	}

	reqBody, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []any{map[string]any{"role": "user", "content": "What is 2+2?"}},
	})

	url := baseURL + "/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21"
	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", "llm-katan-azure-key")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err, "failed to reach llm-katan at %s", baseURL)
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	t.Logf("Raw Azure response (status %d):\n%s", resp.StatusCode, string(respBytes))

	var body map[string]any
	require.NoError(t, json.Unmarshal(respBytes, &body))

	hasPromptFilter := body["prompt_filter_results"] != nil
	hasContentFilter := false
	if choices, ok := body["choices"].([]any); ok {
		for _, raw := range choices {
			if choice, ok := raw.(map[string]any); ok {
				if choice["content_filter_results"] != nil {
					hasContentFilter = true
					break
				}
			}
		}
	}
	t.Logf("Azure-specific fields present: prompt_filter_results=%v, content_filter_results=%v",
		hasPromptFilter, hasContentFilter)

	translatedBody, err := NewAzureOpenAITranslator().TranslateResponse(body, "gpt-4o")
	require.NoError(t, err)

	if hasPromptFilter || hasContentFilter {
		require.NotNil(t, translatedBody, "body should be mutated when Azure fields are present")

		cleanJSON, _ := json.MarshalIndent(translatedBody, "", "  ")
		t.Logf("Cleaned response:\n%s", string(cleanJSON))

		assert.NotContains(t, translatedBody, "prompt_filter_results")
		if choices, ok := translatedBody["choices"].([]any); ok {
			for i, raw := range choices {
				choice := raw.(map[string]any)
				assert.NotContains(t, choice, "content_filter_results",
					"content_filter_results should be stripped from choices[%d]", i)
			}
		}
	} else {
		t.Log("Mock server did not include Azure-specific fields; verifying passthrough")
		assert.Nil(t, translatedBody)
	}

	assert.Contains(t, body, "choices")
	assert.Contains(t, body, "id")
	assert.Contains(t, body, "object")
}
