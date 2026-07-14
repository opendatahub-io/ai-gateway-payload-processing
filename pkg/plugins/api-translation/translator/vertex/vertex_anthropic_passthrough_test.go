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

func newTestPassthroughTranslator() *VertexAnthropicPassthroughTranslator {
	return NewVertexAnthropicPassthroughTranslator()
}

// nativeMessagesBody returns a request body as sent by a native Anthropic
// Messages API client (e.g. Claude Code): already Anthropic-shaped, so the
// passthrough translator must only apply the Vertex deltas.
func nativeMessagesBody() map[string]any {
	return map[string]any{
		"model":      "claude-opus-4-8",
		"max_tokens": float64(1024),
		"system":     "You are a helpful assistant.",
		"messages": []any{
			map[string]any{"role": "user", "content": "What is 2+2?"},
		},
	}
}

func TestVertexAnthropicPassthrough_TranslateRequestWithConfig_BasicChat(t *testing.T) {
	tr := newTestPassthroughTranslator()
	body := nativeMessagesBody()

	translated, headers, headersToRemove, err := tr.TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)

	assert.Equal(t, testAnthropicVersion, translated["anthropic_version"])
	_, hasModel := translated["model"]
	assert.False(t, hasModel, "model must not be in body for Vertex (it is carried in the URL path)")

	// Native Anthropic fields must pass through untouched.
	assert.Equal(t, "You are a helpful assistant.", translated["system"])
	assert.Equal(t, float64(1024), translated["max_tokens"])
	msgs, ok := translated["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 1)
	msg, ok := msgs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user", msg["role"])
	assert.Equal(t, "What is 2+2?", msg["content"])

	assert.Equal(t, "application/json", headers["content-type"])
	_, hasPath := headers[":path"]
	assert.False(t, hasPath, "path is set by applyPathOverride, not by the translator")
	_, hasAnthropicVersionHeader := headers["anthropic-version"]
	assert.False(t, hasAnthropicVersionHeader, "Vertex does not use the anthropic-version header")
	assert.Equal(t, []string{"anthropic-beta"}, headersToRemove, "Vertex rejects unknown anthropic-beta flags, header must be stripped")
}

func TestVertexAnthropicPassthrough_TranslateRequestWithConfig_PreservesRichFields(t *testing.T) {
	tr := newTestPassthroughTranslator()
	body := nativeMessagesBody()
	body["stream"] = true
	body["temperature"] = float64(0.5)
	body["tools"] = []any{
		map[string]any{
			"name":         "get_weather",
			"description":  "Get current weather",
			"input_schema": map[string]any{"type": "object"},
		},
	}
	body["messages"] = []any{
		map[string]any{"role": "user", "content": "Weather in Paris?"},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": map[string]any{"city": "Paris"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "18C, sunny"},
		}},
	}

	translated, _, _, err := tr.TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)

	// The passthrough must not restructure any Anthropic-native content.
	assert.Equal(t, true, translated["stream"])
	assert.Equal(t, float64(0.5), translated["temperature"])
	tools, ok := translated["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	msgs, ok := translated["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 3)
	assistant, ok := msgs[1].(map[string]any)
	require.True(t, ok)
	blocks, ok := assistant["content"].([]any)
	require.True(t, ok)
	block, ok := blocks[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "tool_use", block["type"])
	assert.Equal(t, "toolu_1", block["id"])
}

func TestVertexAnthropicPassthrough_TranslateRequestWithConfig_CustomVersion(t *testing.T) {
	tr := newTestPassthroughTranslator()
	config := map[string]string{AnthropicVersionConfigKey: "vertex-2025-01-01"}

	translated, _, _, err := tr.TranslateRequestWithConfig(nativeMessagesBody(), config)
	require.NoError(t, err)
	assert.Equal(t, "vertex-2025-01-01", translated["anthropic_version"])
}

func TestVertexAnthropicPassthrough_TranslateRequestWithConfig_MissingVersion(t *testing.T) {
	tr := newTestPassthroughTranslator()

	testCases := []struct {
		name   string
		config map[string]string
	}{
		{name: "nil config", config: nil},
		{name: "empty config", config: map[string]string{}},
		{name: "empty value", config: map[string]string{AnthropicVersionConfigKey: ""}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			translated, headers, headersToRemove, err := tr.TranslateRequestWithConfig(nativeMessagesBody(), tc.config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), AnthropicVersionConfigKey)
			assert.Nil(t, translated)
			assert.Nil(t, headers)
			assert.Nil(t, headersToRemove)
		})
	}
}

func TestVertexAnthropicPassthrough_TranslateRequest_RequiresConfig(t *testing.T) {
	tr := newTestPassthroughTranslator()

	translated, headers, headersToRemove, err := tr.TranslateRequest(nativeMessagesBody())
	require.Error(t, err)
	assert.Nil(t, translated)
	assert.Nil(t, headers)
	assert.Nil(t, headersToRemove)
}

func TestVertexAnthropicPassthrough_TranslateResponse_Passthrough(t *testing.T) {
	tr := newTestPassthroughTranslator()

	// Vertex returns native Anthropic Messages responses and the client speaks
	// Messages — a nil translatedBody signals "no mutation" to the plugin.
	translated, err := tr.TranslateResponse(map[string]any{
		"type":        "message",
		"role":        "assistant",
		"content":     []any{map[string]any{"type": "text", "text": "4"}},
		"stop_reason": "end_turn",
	}, "claude-opus-4-8")
	require.NoError(t, err)
	assert.Nil(t, translated, "response must pass through unmodified")
}

func TestVertexAnthropicPassthrough_StripsVertexUnsupportedFields(t *testing.T) {
	tr := NewVertexAnthropicPassthroughTranslator()
	body := nativeMessagesBody()
	body["context_management"] = map[string]any{"edits": []any{map[string]any{"type": "clear_tool_uses_20250919"}}}
	body["mcp_servers"] = []any{map[string]any{"type": "url", "url": "https://example.com"}}
	body["service_tier"] = "auto"

	translated, _, _, err := tr.TranslateRequestWithConfig(body, testConfig())
	require.NoError(t, err)
	for _, f := range vertexUnsupportedBodyFields {
		assert.NotContains(t, translated, f, "Vertex rejects %q with 400 Extra inputs are not permitted", f)
	}
	assert.Contains(t, translated, "messages")
}
