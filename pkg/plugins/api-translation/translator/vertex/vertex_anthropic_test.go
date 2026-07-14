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

const (
	testAnthropicVersion = "vertex-2023-10-16"
)

func testConfig() map[string]string {
	return map[string]string{
		AnthropicVersionConfigKey: testAnthropicVersion,
	}
}

func newTestAnthropicTranslator() *VertexAnthropicTranslator {
	return NewVertexAnthropicTranslator()
}

func TestVertexAnthropic_TranslateRequest_BasicChat(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-4-20250514",
		"messages": []any{
			map[string]any{"role": "user", "content": "What is 2+2?"},
		},
	}

	translated, headers, headersToRemove, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)

	assert.Equal(t, testAnthropicVersion, translated["anthropic_version"])
	_, hasModel := translated["model"]
	assert.False(t, hasModel, "model must not be in body for Vertex")

	msgs := translated["messages"].([]map[string]any)
	require.Len(t, msgs, 1)
	assert.Equal(t, "user", msgs[0]["role"])
	assert.Equal(t, "What is 2+2?", msgs[0]["content"])

	assert.Equal(t, "application/json", headers["content-type"])
	_, hasPath := headers[":path"]
	assert.False(t, hasPath, "path is set by applyPathOverride, not by the translator")
	_, hasAnthropicVersionHeader := headers["anthropic-version"]
	assert.False(t, hasAnthropicVersionHeader, "Vertex does not use anthropic-version header")

	assert.Equal(t, []string{"anthropic-beta"}, headersToRemove, "Vertex rejects unknown anthropic-beta flags, header must be stripped")
}

func TestVertexAnthropic_TranslateRequest_SystemMessage(t *testing.T) {
	body := map[string]any{
		"model": "claude-opus-4-7",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are a helpful assistant."},
			map[string]any{"role": "user", "content": "Hello"},
		},
	}

	translated, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)

	assert.Equal(t, "You are a helpful assistant.", translated["system"])
	assert.Equal(t, testAnthropicVersion, translated["anthropic_version"])
}

func TestVertexAnthropic_TranslateRequest_MultipleMessages(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-4-20250514",
		"messages": []any{
			map[string]any{"role": "system", "content": "Be helpful."},
			map[string]any{"role": "user", "content": "Hi"},
			map[string]any{"role": "assistant", "content": "Hello!"},
			map[string]any{"role": "user", "content": "How are you?"},
		},
	}

	translated, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)

	msgs := translated["messages"].([]map[string]any)
	require.Len(t, msgs, 3, "system message should be extracted, leaving 3 conversation messages")
	assert.Equal(t, "Be helpful.", translated["system"])
}

func TestVertexAnthropic_TranslateRequest_AnthropicVersionFromConfig(t *testing.T) {
	body := map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	config := map[string]string{
		AnthropicVersionConfigKey: "vertex-2025-01-01",
	}
	translated, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, config)
	require.NoError(t, err)
	assert.Equal(t, "vertex-2025-01-01", translated["anthropic_version"])
}

func TestVertexAnthropic_TranslateRequest_MissingAnthropicVersion(t *testing.T) {
	body := map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, map[string]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), AnthropicVersionConfigKey)
}

func TestVertexAnthropic_TranslateRequest_NilConfig(t *testing.T) {
	body := map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), AnthropicVersionConfigKey)
}

func TestVertexAnthropic_TranslateRequest_MissingModel(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}

func TestVertexAnthropic_TranslateRequest_MissingMessages(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-4-20250514",
	}

	_, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	assert.Error(t, err)
}

func TestVertexAnthropic_TranslateRequest_StreamForwarded(t *testing.T) {
	body := map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
		"stream":   true,
	}

	translated, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)
	assert.Equal(t, true, translated["stream"])
}

func TestVertexAnthropic_TranslateRequest_ModelRemovedFromBody(t *testing.T) {
	body := map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	translated, _, _, err := newTestAnthropicTranslator().TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)
	_, hasModel := translated["model"]
	assert.False(t, hasModel, "model must be removed from body for Vertex Anthropic")
}

func TestVertexAnthropic_TranslateRequest_BaseMethodReturnsError(t *testing.T) {
	body := map[string]any{
		"model":    "claude-sonnet-4-20250514",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	_, _, _, err := newTestAnthropicTranslator().TranslateRequest(body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TranslateRequestWithConfig")
}

func TestVertexAnthropic_TranslateResponse_BasicCompletion(t *testing.T) {
	body := map[string]any{
		"id":    "msg_123",
		"type":  "message",
		"model": "claude-sonnet-4-20250514",
		"content": []any{
			map[string]any{"type": "text", "text": "The answer is 4."},
		},
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens":  float64(10),
			"output_tokens": float64(5),
		},
	}

	translated, err := newTestAnthropicTranslator().TranslateResponse(body, "claude-sonnet-4-20250514")
	require.NoError(t, err)

	assert.Equal(t, "msg_123", translated["id"])
	assert.Equal(t, "chat.completion", translated["object"])
	assert.Equal(t, "claude-sonnet-4-20250514", translated["model"])

	choices := translated["choices"].([]any)
	require.Len(t, choices, 1)
	choice := choices[0].(map[string]any)
	assert.Equal(t, "stop", choice["finish_reason"])

	msg := choice["message"].(map[string]any)
	assert.Equal(t, "The answer is 4.", msg["content"])
}

func TestVertexAnthropic_TranslateResponse_ToolUse(t *testing.T) {
	body := map[string]any{
		"type": "message",
		"content": []any{
			map[string]any{"type": "text", "text": "Checking weather."},
			map[string]any{
				"type":  "tool_use",
				"id":    "toolu_abc",
				"name":  "get_weather",
				"input": map[string]any{"location": "SF"},
			},
		},
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": float64(1), "output_tokens": float64(1)},
	}

	translated, err := newTestAnthropicTranslator().TranslateResponse(body, "test")
	require.NoError(t, err)

	choices := translated["choices"].([]any)
	choice := choices[0].(map[string]any)
	assert.Equal(t, "tool_calls", choice["finish_reason"])
}
