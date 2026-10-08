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
	"fmt"
	"sort"
	"strings"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
)

// extractMessages returns user-supplied text as a message slice suitable for NeMo's
// OpenAI-compatible chat endpoint. It supports three payload formats:
//
//  1. OpenAI chat: top-level "messages" array → forwards all messages.
//  2. OpenResponses: "instructions" + "input" (string or item array) → normalised messages.
//  3. MCP JSON-RPC: {"jsonrpc":"2.0","params":{"arguments":{…}}} → concatenates
//     all string argument values into a single user message.
//
// When inputFormat is OpenAIResponses, the OpenResponses path is used. Otherwise the
// body is inspected for known fields, with OpenResponses "input" as a fallback.
//
// Returns (nil, nil) when no content is found.
func extractMessages(body map[string]any, inputFormat apiformat.APIFormat) ([]any, error) {
	if raw, ok := body["messages"]; ok {
		return extractOpenAIMessages(raw)
	}
	if _, ok := body["jsonrpc"]; ok {
		return extractMCPArguments(body)
	}
	if _, ok := body["input"]; ok {
		return extractOpenResponsesInput(body)
	}
	return nil, nil
}

// extractOpenAIMessages parses an OpenAI-style "messages" value. All messages are forwarded
// so NeMo can evaluate the full conversation context.
func extractOpenAIMessages(raw any) ([]any, error) {
	slice, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("messages is not an array")
	}
	if len(slice) == 0 {
		return nil, nil
	}
	return slice, nil
}

// extractMCPArguments extracts text from an MCP JSON-RPC tools/call
// payload. String values inside params.arguments are sorted by key and joined
// into a single "user" message so NeMo can evaluate them with input rails.
func extractMCPArguments(body map[string]any) ([]any, error) {
	params, ok := body["params"].(map[string]any)
	if !ok {
		return nil, nil
	}
	args, ok := params["arguments"].(map[string]any)
	if !ok {
		return nil, nil
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		if s, ok := args[k].(string); ok {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return []any{map[string]string{"role": "user", "content": strings.Join(parts, "\n")}}, nil
}

// extractOpenResponsesInput converts an OpenResponses request body into NeMo-compatible messages.
func extractOpenResponsesInput(body map[string]any) ([]any, error) {
	var messages []any

	if instructions, ok := body["instructions"].(string); ok && instructions != "" {
		messages = append(messages, map[string]string{"role": "system", "content": instructions})
	}

	rawInput, ok := body["input"]
	if !ok {
		if len(messages) == 0 {
			return nil, nil
		}
		return messages, nil
	}

	switch v := rawInput.(type) {
	case string:
		if v == "" {
			if len(messages) == 0 {
				return nil, nil
			}
			return messages, nil
		}
		messages = append(messages, map[string]string{"role": "user", "content": v})
		return messages, nil
	case []any:
		itemMessages, err := extractOpenResponsesInputItems(v)
		if err != nil {
			return nil, err
		}
		messages = append(messages, itemMessages...)
		if len(messages) == 0 {
			return nil, nil
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("input has unsupported type")
	}
}

func extractOpenResponsesInputItems(items []any) ([]any, error) {
	var messages []any
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input[%d] has unsupported type", i)
		}
		itemType, _ := item["type"].(string)
		switch itemType {
		case "message":
			role, _ := item["role"].(string)
			if role == "" {
				role = "user"
			}
			content, err := extractOpenResponsesTextContent(item["content"])
			if err != nil {
				return nil, fmt.Errorf("input[%d]: %w", i, err)
			}
			if content == "" {
				continue
			}
			messages = append(messages, map[string]string{"role": role, "content": content})
		case "function_call":
			name, _ := item["name"].(string)
			args, _ := item["arguments"].(string)
			if name == "" && args == "" {
				continue
			}
			messages = append(messages, map[string]string{
				"role":    "assistant",
				"content": fmt.Sprintf("%s(%s)", name, args),
			})
		case "function_call_output":
			output, _ := item["output"].(string)
			if output == "" {
				continue
			}
			messages = append(messages, map[string]string{"role": "tool", "content": output})
		}
	}
	return messages, nil
}

// extractAssistantMessages extracts assistant content from a response body.
// It supports three payload formats:
//  1. OpenAI chat (via "choices"): choices (fail closed), and nil when no content is found.
//  2. OpenResponses (via "output"): assistant message items with nested text content.
//  3. MCP JSON-RPC: {"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"Hello"}]}}
//
// Returns (nil, nil) when no content is found.
func extractAssistantMessages(body map[string]any, inputFormat apiformat.APIFormat) ([]map[string]string, error) {
	if inputFormat == apiformat.OpenAIResponses {
		return extractOpenResponsesOutput(body)
	}
	if raw, ok := body["choices"]; ok {
		return extractOpenAIAssistantMessagesFromChoices(raw)
	}
	if _, ok := body["jsonrpc"]; ok {
		return extractMCPTextContent(body)
	}
	if _, ok := body["output"]; ok {
		return extractOpenResponsesOutput(body)
	}
	return nil, nil
}

// extractOpenAIAssistantMessagesFromChoices parses OpenAI-style choices into assistant messages.
func extractOpenAIAssistantMessagesFromChoices(raw any) ([]map[string]string, error) {
	choiceSlice, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("choices field has unsupported type")
	}
	if len(choiceSlice) == 0 {
		return nil, nil
	}

	var messages []map[string]string
	for i, choice := range choiceSlice {
		choiceMap, ok := choice.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("choice[%d] has unsupported type", i)
		}
		msg, ok := choiceMap["message"].(map[string]any)
		if !ok {
			msg, ok = choiceMap["delta"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("choice[%d] has no message or delta field", i)
			}
		}
		rawContent, exists := msg["content"]
		if !exists || rawContent == nil {
			continue
		}
		content, ok := rawContent.(string)
		if !ok {
			return nil, fmt.Errorf("choice[%d] content is not a string", i)
		}
		if content == "" {
			continue
		}
		messages = append(messages, map[string]string{"role": "assistant", "content": content})
	}
	return messages, nil
}

// extractOpenResponsesOutput extracts assistant text from an OpenResponses response body.
func extractOpenResponsesOutput(body map[string]any) ([]map[string]string, error) {
	rawOutput, ok := body["output"]
	if !ok {
		return nil, nil
	}
	outputSlice, ok := rawOutput.([]any)
	if !ok {
		return nil, fmt.Errorf("output is not an array")
	}
	if len(outputSlice) == 0 {
		return nil, nil
	}

	var messages []map[string]string
	for i, raw := range outputSlice {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("output[%d] has unsupported type", i)
		}
		if itemType, _ := item["type"].(string); itemType != "message" {
			continue
		}
		role, _ := item["role"].(string)
		if role != "" && role != "assistant" {
			continue
		}
		content, err := extractOpenResponsesTextContent(item["content"])
		if err != nil {
			return nil, fmt.Errorf("output[%d]: %w", i, err)
		}
		if content == "" {
			continue
		}
		messages = append(messages, map[string]string{"role": "assistant", "content": content})
	}
	return messages, nil
}

// extractOpenResponsesTextContent extracts plain text from OpenResponses content fields,
// which may be a string or an array of typed content blocks.
func extractOpenResponsesTextContent(raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case []any:
		var parts []string
		for i, block := range v {
			blockMap, ok := block.(map[string]any)
			if !ok {
				return "", fmt.Errorf("content[%d] has unsupported type", i)
			}
			blockType, _ := blockMap["type"].(string)
			switch blockType {
			case "output_text", "input_text", "text":
				if text, ok := blockMap["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " "), nil
	default:
		return "", fmt.Errorf("content has unsupported type")
	}
}

// extractMCPTextContent parses MCP text content into assistant messages.
func extractMCPTextContent(body map[string]any) ([]map[string]string, error) {
	result, _ := body["result"].(map[string]any)
	contentSlice, _ := result["content"].([]any)
	var messages []map[string]string
	for _, item := range contentSlice {
		entry, _ := item.(map[string]any)
		if entry["type"] != "text" {
			continue
		}
		text, ok := entry["text"].(string)
		if !ok {
			return nil, fmt.Errorf("mcp text content is not a string")
		}
		if text != "" {
			messages = append(messages, map[string]string{"role": "assistant", "content": text})
		}
	}
	return messages, nil
}
