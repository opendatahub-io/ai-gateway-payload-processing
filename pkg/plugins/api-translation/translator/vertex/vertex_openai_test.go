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

package vertex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVertexOpenAI_TranslateRequest_PassthroughAllChatParams(t *testing.T) {
	body := map[string]any{
		"model": "google/gemini-2.0-flash",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are helpful."},
			map[string]any{"role": "user", "content": "Hello"},
		},
		"temperature":       0.7,
		"top_p":             0.9,
		"max_tokens":        1000,
		"stream":            true,
		"stop":              []any{"END"},
		"n":                 1,
		"presence_penalty":  0.5,
		"frequency_penalty": 0.3,
	}

	translatedBody, headers, headersToRemove, err := NewVertexOpenAITranslator().TranslateRequest(body)
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "Vertex OpenAI-compatible API should not mutate the request body")
	assert.Equal(t, "application/json", headers["content-type"])
	assert.NotContains(t, headers, ":path", "path is set by applyPathOverride, not the translator")
	assert.Len(t, headers, 1)
	assert.Nil(t, headersToRemove)
}

func TestVertexOpenAI_TranslateRequest_MissingModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := NewVertexOpenAITranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
}

func TestVertexOpenAI_TranslateRequest_EmptyModel(t *testing.T) {
	body := map[string]any{
		"model":    "",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := NewVertexOpenAITranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
}

func TestVertexOpenAI_TranslateRequest_MissingMessages(t *testing.T) {
	body := map[string]any{
		"model": "gemini-2.5-flash",
	}

	_, _, _, err := NewVertexOpenAITranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "messages field is required and must not be empty")
}

func TestVertexOpenAI_TranslateRequest_EmptyMessages(t *testing.T) {
	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{},
	}

	_, _, _, err := NewVertexOpenAITranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "messages field is required and must not be empty")
}

func TestVertexOpenAI_TranslateResponse_NoExtraProperties(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "google/gemini-2.0-flash",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "The answer is 4.",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 5,
			"total_tokens":      15,
		},
	}

	translatedBody, err := NewVertexOpenAITranslator().TranslateResponse(body, "google/gemini-2.0-flash")
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "Response without extra_properties should not be mutated")
}

func TestVertexOpenAI_TranslateResponse_StripsExtraProperties(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-vertex-test-001",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "google/gemini-2.5-flash",
		"choices": []any{
			map[string]any{
				"index":    0,
				"logprobs": nil,
				"message": map[string]any{
					"role":    "assistant",
					"content": "test response",
				},
				"finish_reason": "stop",
			},
		},
		"system_fingerprint": "",
		"usage": map[string]any{
			"prompt_tokens":     5,
			"completion_tokens": 2,
			"total_tokens":      23,
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": 16,
			},
			"extra_properties": map[string]any{
				"google": map[string]any{
					"traffic_type": "ON_DEMAND",
				},
			},
		},
	}

	translatedBody, err := NewVertexOpenAITranslator().TranslateResponse(body, "google/gemini-2.5-flash")
	require.NoError(t, err)
	require.NotNil(t, translatedBody, "Response with extra_properties should be mutated")
	usage := translatedBody["usage"].(map[string]any)
	assert.NotContains(t, usage, "extra_properties", "extra_properties should be stripped")
	assert.Contains(t, usage, "prompt_tokens", "standard fields should be preserved")
	assert.Contains(t, usage, "completion_tokens_details", "completion_tokens_details is standard OpenAI")
}

func TestVertexOpenAI_TranslateResponse_ErrorPassthrough(t *testing.T) {
	body := map[string]any{
		"error": map[string]any{
			"message": "Model not found",
			"type":    "invalid_request_error",
			"code":    "model_not_found",
		},
	}

	translatedBody, err := NewVertexOpenAITranslator().TranslateResponse(body, "invalid-model")
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "Error responses should pass through unchanged")
}
