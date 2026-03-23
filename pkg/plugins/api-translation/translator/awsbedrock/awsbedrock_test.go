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

package awsbedrock

import (
	"testing"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslateRequest_BasicChat(t *testing.T) {
	body := map[string]any{
		"model": "nvidia.nemotron-nano-12b-v2",
		"messages": []any{
			map[string]any{"role": "user", "content": "What is 2+2?"},
		},
	}

	translatedBody, headers, headersToRemove, err := NewBedrockTranslator().TranslateRequest(body)
	require.NoError(t, err)

	assert.Nil(t, translatedBody, "body should not be mutated for Bedrock OpenAI-compatible API")
	assert.Equal(t, "/v1/chat/completions", headers[":path"])
	assert.Equal(t, "application/json", headers["content-type"])
	assert.Empty(t, headersToRemove)
}

func TestTranslateRequest_PassthroughAllChatParams(t *testing.T) {
	body := map[string]any{
		"model": "nvidia.nemotron-nano-12b-v2",
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

	translatedBody, _, _, err := NewBedrockTranslator().TranslateRequest(body)
	require.NoError(t, err)

	assert.Nil(t, translatedBody, "Bedrock OpenAI-compatible API should not mutate the request body")
}

func TestTranslateRequest_MissingModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := NewBedrockTranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
}

func TestTranslateRequest_EmptyModel(t *testing.T) {
	body := map[string]any{
		"model":    "",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := NewBedrockTranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model field is required")
}

func TestTranslateRequest_MissingMessages(t *testing.T) {
	body := map[string]any{
		"model": "nvidia.nemotron-nano-12b-v2",
		// missing "messages" field
	}

	_, _, _, err := NewBedrockTranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "messages field is required for chat completions")
}

func TestTranslateRequest_MessagesNotArray(t *testing.T) {
	body := map[string]any{
		"model":    "nvidia.nemotron-nano-12b-v2",
		"messages": "not an array", // wrong type
	}

	_, _, _, err := NewBedrockTranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "messages field must be an array")
}

func TestTranslateRequest_EmptyMessagesArray(t *testing.T) {
	body := map[string]any{
		"model":    "nvidia.nemotron-nano-12b-v2",
		"messages": []any{}, // empty array
	}

	_, _, _, err := NewBedrockTranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "messages array cannot be empty")
}

func TestTranslateRequest_HeadersSet(t *testing.T) {
	body := map[string]any{
		"model":    "nvidia.nemotron-nano-12b-v2",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, headers, _, err := NewBedrockTranslator().TranslateRequest(body)
	require.NoError(t, err)

	assert.Len(t, headers, 2)
	assert.Contains(t, headers, ":path")
	assert.Contains(t, headers, "content-type")
}

func TestTranslateResponse_Passthrough(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "nvidia.nemotron-nano-12b-v2",
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

	translatedBody, err := NewBedrockTranslator().TranslateResponse(body, "nvidia.nemotron-nano-12b-v2")
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "Bedrock OpenAI-compatible response should not be mutated")
}

func TestTranslateResponse_StreamingChunk(t *testing.T) {
	body := map[string]any{
		"id":      "chatcmpl-abc123",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "nvidia.nemotron-nano-12b-v2",
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{
					"content": "Hello",
				},
				"finish_reason": nil,
			},
		},
	}

	translatedBody, err := NewBedrockTranslator().TranslateResponse(body, "nvidia.nemotron-nano-12b-v2")
	require.NoError(t, err)
	assert.Nil(t, translatedBody)
}

func TestTranslateResponse_Error(t *testing.T) {
	body := map[string]any{
		"error": map[string]any{
			"message": "Model not found",
			"type":    "invalid_request_error",
			"code":    "model_not_found",
		},
	}

	translatedBody, err := NewBedrockTranslator().TranslateResponse(body, "invalid-model")
	require.NoError(t, err)
	assert.Nil(t, translatedBody, "Error responses should pass through unchanged")
}

func TestProviderName(t *testing.T) {
	assert.Equal(t, "awsbedrock-openai", provider.AWSBedrockOpenAI)
}

func TestNewBedrockTranslator(t *testing.T) {
	translator := NewBedrockTranslator()
	assert.NotNil(t, translator)
	assert.Equal(t, OpenAiApi, translator.apiType)
}
