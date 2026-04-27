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

package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
)

const (
	anthropicAPIVersion = "2023-06-01"
	anthropicPath       = "/v1/messages"
	defaultMaxTokens    = 4096
)

// compile-time interface check
var _ translator.Translator = &AnthropicTranslator{}

// NewAnthropicTranslator initializes a new AnthropicTranslator and returns its pointer.
func NewAnthropicTranslator() *AnthropicTranslator {
	return &AnthropicTranslator{}
}

// AnthropicTranslator translates between OpenAI Chat Completions format and
// Anthropic Messages API format. Works with generic map[string]any bodies.
type AnthropicTranslator struct{}

// TranslateRequest translates an OpenAI Chat Completions request body to Anthropic Messages API format.
func (t *AnthropicTranslator) TranslateRequest(body map[string]any) (map[string]any, map[string]string, []string, error) {
	model, _ := body["model"].(string)
	if model == "" {
		return nil, nil, nil, errcommon.Error{Code: errcommon.BadRequest, Msg: "model field is required"}
	}

	messages, err := extractMessages(body)
	if err != nil {
		return nil, nil, nil, errcommon.Error{Code: errcommon.BadRequest, Msg: fmt.Sprintf("failed to extract messages: %v", err)}
	}

	systemPrompt, anthropicMessages, err := separateSystemMessages(messages)
	if err != nil {
		return nil, nil, nil, errcommon.Error{Code: errcommon.BadRequest, Msg: err.Error()}
	}

	if len(anthropicMessages) == 0 {
		return nil, nil, nil, errcommon.Error{Code: errcommon.BadRequest, Msg: "at least one non-system message is required"}
	}

	maxTokens := resolveMaxTokens(body)

	translated := map[string]any{
		"model":      model,
		"messages":   anthropicMessages,
		"max_tokens": maxTokens,
	}

	if systemPrompt != "" {
		translated["system"] = systemPrompt
	}

	if temp, ok := getFloat(body, "temperature"); ok {
		translated["temperature"] = temp
	}
	if topP, ok := getFloat(body, "top_p"); ok {
		translated["top_p"] = topP
	}
	if stop := extractStopSequences(body); len(stop) > 0 {
		translated["stop_sequences"] = stop
	}

	if tools := translateToolDefinitions(body); len(tools) > 0 {
		translated["tools"] = tools
		if toolChoice := translateToolChoice(body); toolChoice != nil {
			translated["tool_choice"] = toolChoice
		}
	}

	if stream, ok := body["stream"].(bool); ok {
		translated["stream"] = stream
	}

	if responseFormat, ok := body["response_format"]; ok {
		translated["response_format"] = responseFormat
	}

	headers := map[string]string{
		"anthropic-version": anthropicAPIVersion,
		"content-type":      "application/json",
		":path":             anthropicPath,
	}

	return translated, headers, nil, nil
}

// TranslateResponse translates an Anthropic Messages API response to OpenAI Chat Completions format.
// Handles both success responses (type: "message") and error responses (type: "error").
func (t *AnthropicTranslator) TranslateResponse(body map[string]any, model string) (map[string]any, error) {
	bodyType, _ := body["type"].(string)

	// Handle Anthropic error responses
	if bodyType == "error" {
		return translateAnthropicError(body), nil
	}

	content := extractAnthropicContent(body)
	finishReason := mapStopReason(body)
	usage := mapAnthropicUsage(body)

	id, _ := body["id"].(string)
	if model == "" {
		model, _ = body["model"].(string)
	}

	message := map[string]any{
		"role":    "assistant",
		"content": content,
	}

	// Extract tool_use blocks if stop_reason is tool_use
	if finishReason == "tool_calls" {
		toolCalls, err := extractAnthropicToolCalls(body)
		if err != nil {
			return nil, fmt.Errorf("failed to extract tool calls: %w", err)
		}
		if len(toolCalls) > 0 {
			message["tool_calls"] = toolCalls
		}
	}

	translated := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": usage,
	}

	return translated, nil
}

// translateAnthropicError converts an Anthropic error response to OpenAI error format.
func translateAnthropicError(body map[string]any) map[string]any {
	errObj, _ := body["error"].(map[string]any)
	errType, _ := errObj["type"].(string)
	errMessage, _ := errObj["message"].(string)

	return map[string]any{
		"error": map[string]any{
			"message": errMessage,
			"type":    errType,
			"param":   nil,
			"code":    errType,
		},
	}
}

// separateSystemMessages extracts the system prompt and returns non-system messages
// in Anthropic format (with role and content fields).
// Maps OpenAI "developer" role to Anthropic system prompt (concatenated with "system").
// Translates "tool" role messages to Anthropic "tool_result" content blocks in a "user" message.
// Translates assistant messages with "tool_calls" to Anthropic "tool_use" content blocks.
func separateSystemMessages(messages []map[string]any) (string, []map[string]any, error) {
	var systemParts []string
	var anthropicMessages []map[string]any

	for i, msg := range messages {
		role, _ := msg["role"].(string)

		switch role {
		case "system", "developer":
			content := extractContentString(msg)
			systemParts = append(systemParts, content)
		case "user":
			content := extractContentString(msg)
			anthropicMessages = append(anthropicMessages, map[string]any{
				"role":    "user",
				"content": content,
			})
		case "assistant":
			anthropicMessages = append(anthropicMessages, buildAssistantMessage(msg))
		case "tool":
			toolResultMsg, err := buildToolResultMessage(msg)
			if err != nil {
				return "", nil, fmt.Errorf("message at index %d: %w", i, err)
			}
			anthropicMessages = appendToolResult(anthropicMessages, toolResultMsg)
		default:
			return "", nil, fmt.Errorf("message at index %d has unknown role '%s'", i, role)
		}
	}

	return joinStrings(systemParts, "\n"), anthropicMessages, nil
}

// buildAssistantMessage converts an OpenAI assistant message to Anthropic format.
// If the message has tool_calls, they are translated to Anthropic tool_use content blocks.
func buildAssistantMessage(msg map[string]any) map[string]any {
	toolCalls, hasToolCalls := msg["tool_calls"].([]any)
	if !hasToolCalls || len(toolCalls) == 0 {
		return map[string]any{
			"role":    "assistant",
			"content": extractContentString(msg),
		}
	}

	var contentBlocks []any

	// Add text content block if present
	if text := extractContentString(msg); text != "" {
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "text",
			"text": text,
		})
	}

	// Convert each OpenAI tool_call to an Anthropic tool_use block
	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]any)
		if !ok {
			continue
		}
		toolUse := translateToolCallToToolUse(tcMap)
		if toolUse != nil {
			contentBlocks = append(contentBlocks, toolUse)
		}
	}

	return map[string]any{
		"role":    "assistant",
		"content": contentBlocks,
	}
}

// translateToolCallToToolUse converts an OpenAI tool_call object to an Anthropic tool_use content block.
func translateToolCallToToolUse(toolCall map[string]any) map[string]any {
	id, _ := toolCall["id"].(string)
	fn, ok := toolCall["function"].(map[string]any)
	if !ok {
		return nil
	}
	name, _ := fn["name"].(string)

	// Parse arguments from JSON string to map
	var input any
	if argsStr, ok := fn["arguments"].(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(argsStr), &parsed); err == nil {
			input = parsed
		} else {
			input = map[string]any{}
		}
	} else {
		// Already a map (e.g., from in-memory representation)
		input = fn["arguments"]
		if input == nil {
			input = map[string]any{}
		}
	}

	return map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": input,
	}
}

// buildToolResultMessage converts an OpenAI "tool" role message to an Anthropic
// "tool_result" content block wrapped in a "user" message.
func buildToolResultMessage(msg map[string]any) (map[string]any, error) {
	toolCallID, _ := msg["tool_call_id"].(string)
	if toolCallID == "" {
		return nil, fmt.Errorf("tool message missing required 'tool_call_id' field")
	}

	content := extractContentString(msg)

	toolResult := map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolCallID,
	}
	if content != "" {
		toolResult["content"] = content
	}

	return toolResult, nil
}

// appendToolResult appends a tool_result content block to the message list.
// If the last message is already a "user" message with content blocks (from a previous
// tool result), the new block is merged into it. This handles the common pattern of
// multiple consecutive tool results that Anthropic expects in a single user message.
func appendToolResult(messages []map[string]any, toolResult map[string]any) []map[string]any {
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if role, _ := last["role"].(string); role == "user" {
			if blocks, ok := last["content"].([]any); ok {
				last["content"] = append(blocks, toolResult)
				return messages
			}
		}
	}
	return append(messages, map[string]any{
		"role":    "user",
		"content": []any{toolResult},
	})
}

// extractMessages extracts the messages array from the request body.
func extractMessages(body map[string]any) ([]map[string]any, error) {
	rawMessages, ok := body["messages"]
	if !ok {
		return nil, fmt.Errorf("messages field is required")
	}

	messagesSlice, ok := rawMessages.([]any)
	if !ok {
		return nil, fmt.Errorf("messages must be an array")
	}

	var messages []map[string]any
	for i, raw := range messagesSlice {
		msg, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message at index %d is not an object", i)
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// extractContentString extracts text content from a message, handling both
// string content and array-of-content-parts formats.
// Only text content is extracted; non-text blocks (image_url, audio, etc.) are skipped.
// Returns empty string if no text content is found.
func extractContentString(msg map[string]any) string {
	content, ok := msg["content"]
	if !ok {
		return ""
	}

	// Simple string content
	if s, ok := content.(string); ok {
		return s
	}

	// Array of content parts (e.g., [{type: "text", text: "hello"}])
	if parts, ok := content.([]any); ok {
		var texts []string
		for _, part := range parts {
			if partMap, ok := part.(map[string]any); ok {
				if text, ok := partMap["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		return joinStrings(texts, " ")
	}

	return ""
}

// resolveMaxTokens extracts max tokens from the request body, checking
// max_completion_tokens first, then max_tokens, defaulting to 4096.
func resolveMaxTokens(body map[string]any) int {
	if v, ok := getInt(body, "max_completion_tokens"); ok && v > 0 {
		return v
	}
	if v, ok := getInt(body, "max_tokens"); ok && v > 0 {
		return v
	}
	return defaultMaxTokens
}

// extractStopSequences extracts stop sequences from the request body,
// handling both string and array formats.
func extractStopSequences(body map[string]any) []string {
	stop, ok := body["stop"]
	if !ok {
		return nil
	}

	if s, ok := stop.(string); ok && s != "" {
		return []string{s}
	}

	if arr, ok := stop.([]any); ok {
		var sequences []string
		for _, v := range arr {
			if s, ok := v.(string); ok {
				sequences = append(sequences, s)
			}
		}
		return sequences
	}

	return nil
}

// extractAnthropicContent extracts text from Anthropic response content blocks.
func extractAnthropicContent(body map[string]any) string {
	contentBlocks, ok := body["content"].([]any)
	if !ok {
		return ""
	}

	var texts []string
	for _, block := range contentBlocks {
		if blockMap, ok := block.(map[string]any); ok {
			if blockType, _ := blockMap["type"].(string); blockType == "text" {
				if text, ok := blockMap["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
	}

	return joinStrings(texts, "")
}

// extractAnthropicToolCalls extracts tool_use blocks from an Anthropic response
// and converts them to OpenAI tool_calls format.
func extractAnthropicToolCalls(body map[string]any) ([]any, error) {
	contentBlocks, ok := body["content"].([]any)
	if !ok {
		return nil, nil
	}

	var toolCalls []any
	toolIndex := 0
	for _, block := range contentBlocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if blockType, _ := blockMap["type"].(string); blockType == "tool_use" {
			id, _ := blockMap["id"].(string)
			name, _ := blockMap["name"].(string)
			input := blockMap["input"]

			args, err := toJSONString(input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool call arguments for '%s' - %w", name, err)
			}

			toolCall := map[string]any{
				"id":    id,
				"index": toolIndex,
				"type":  "function",
				"function": map[string]any{
					"name":      name,
					"arguments": args,
				},
			}
			toolCalls = append(toolCalls, toolCall)
			toolIndex++
		}
	}

	return toolCalls, nil
}

// mapStopReason maps Anthropic stop_reason to OpenAI finish_reason.
func mapStopReason(body map[string]any) string {
	reason, _ := body["stop_reason"].(string)
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// mapAnthropicUsage maps Anthropic usage fields to OpenAI format.
func mapAnthropicUsage(body map[string]any) map[string]any {
	usage, ok := body["usage"].(map[string]any)
	if !ok {
		return map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		}
	}

	inputTokens := toInt(usage["input_tokens"])
	outputTokens := toInt(usage["output_tokens"])

	return map[string]any{
		"prompt_tokens":     inputTokens,
		"completion_tokens": outputTokens,
		"total_tokens":      inputTokens + outputTokens,
	}
}

// translateToolDefinitions converts OpenAI tools[] to Anthropic tools[] format.
// OpenAI: {"type":"function","function":{"name":"X","description":"Y","parameters":{...}}}
// Anthropic: {"name":"X","description":"Y","input_schema":{...}}
func translateToolDefinitions(body map[string]any) []any {
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		return nil
	}

	var anthropicTools []any
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := toolMap["function"].(map[string]any)
		if !ok {
			continue
		}

		anthropicTool := map[string]any{
			"name": fn["name"],
		}
		if desc, ok := fn["description"].(string); ok && desc != "" {
			anthropicTool["description"] = desc
		}
		if params, ok := fn["parameters"]; ok && params != nil {
			anthropicTool["input_schema"] = params
		} else {
			// Anthropic requires input_schema; default to empty object schema
			anthropicTool["input_schema"] = map[string]any{"type": "object"}
		}

		anthropicTools = append(anthropicTools, anthropicTool)
	}

	return anthropicTools
}

// translateToolChoice converts OpenAI tool_choice to Anthropic tool_choice format.
// OpenAI "auto" → Anthropic {"type":"auto"}
// OpenAI "required" → Anthropic {"type":"any"}
// OpenAI "none" → Anthropic: omitted (return nil)
// OpenAI {"type":"function","function":{"name":"X"}} → Anthropic {"type":"tool","name":"X"}
func translateToolChoice(body map[string]any) map[string]any {
	toolChoice, ok := body["tool_choice"]
	if !ok {
		return nil
	}

	if s, ok := toolChoice.(string); ok {
		switch s {
		case "auto":
			return map[string]any{"type": "auto"}
		case "required":
			return map[string]any{"type": "any"}
		default:
			// "none" or unknown — omit tool_choice, letting Anthropic use default behavior
			return nil
		}
	}

	if tcMap, ok := toolChoice.(map[string]any); ok {
		if fn, ok := tcMap["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]any{"type": "tool", "name": name}
			}
		}
	}

	return nil
}

// Helper functions for type-safe extraction from map[string]any

func getFloat(body map[string]any, key string) (float64, bool) {
	v, ok := body[key]
	if !ok {
		return 0, false
	}
	switch f := v.(type) {
	case float64:
		return f, true
	case int:
		return float64(f), true
	case int64:
		return float64(f), true
	default:
		return 0, false
	}
}

func getInt(body map[string]any, key string) (int, bool) {
	v, ok := body[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func toJSONString(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal to JSON: %w", err)
	}
	return string(b), nil
}

func joinStrings(parts []string, sep string) string {
	return strings.Join(parts, sep)
}
