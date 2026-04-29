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

	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
)

const (
	// NemoResponseGuardPluginType is the plugin type identifier.
	NemoResponseGuardPluginType = "nemo-response-guard"
)

// compile-time type validation
var _ framework.ResponseProcessor = &NemoResponseGuardPlugin{}

// NemoResponseGuardPlugin calls a NeMo Guardrails service over HTTP to check model output
// using output rails. It implements ResponseProcessor to inspect responses before returning
// them to the caller.
type NemoResponseGuardPlugin struct {
	typedName plugin.TypedName
	nemoGuardBase
}

// NemoResponseGuardFactory is the factory function for NemoResponseGuardPlugin.
func NemoResponseGuardFactory(name string, rawParameters json.RawMessage, _ framework.Handle) (framework.BBRPlugin, error) {
	config := nemoGuardConfig{
		TimeoutSeconds: defaultTimeoutSec,
	}

	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &config); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of '%s' plugin - %w", NemoResponseGuardPluginType, err)
		}
	}

	plugin, err := NewNemoResponseGuardPlugin(config.NemoURL, config.TimeoutSeconds)
	if err != nil {
		return nil, fmt.Errorf("failed to create '%s' plugin - %w", NemoResponseGuardPluginType, err)
	}

	return plugin.WithName(name), nil
}

// NewNemoResponseGuardPlugin builds a NeMo response guard plugin from validated parameters.
func NewNemoResponseGuardPlugin(nemoURL string, timeoutSeconds int) (*NemoResponseGuardPlugin, error) {
	base, err := newNemoGuardBase(nemoURL, timeoutSeconds)
	if err != nil {
		return nil, err
	}
	return &NemoResponseGuardPlugin{
		typedName:     plugin.TypedName{Type: NemoResponseGuardPluginType, Name: NemoResponseGuardPluginType},
		nemoGuardBase: *base,
	}, nil
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *NemoResponseGuardPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the name of the plugin instance.
func (p *NemoResponseGuardPlugin) WithName(name string) *NemoResponseGuardPlugin {
	p.typedName.Name = name
	return p
}

// ProcessResponse calls NeMo Guardrails to evaluate output rails on the model response.
// It extracts assistant messages from the OpenAI-style response body, POSTs them to
// NeMo's /v1/guardrail/checks endpoint, and returns an errcommon.Error with Forbidden (403)
// if NeMo flags the content.
//
// NeMo always returns HTTP 200 for both allowed and blocked responses. The block/allow
// decision is conveyed through the response body "status" field.
// "success" means the response passed all rails; "blocked" means it is blocked.
func (p *NemoResponseGuardPlugin) ProcessResponse(ctx context.Context, _ *framework.CycleState, response *framework.InferenceResponse) error {
	messages, err := extractAssistantMessages(response.Body)
	if err != nil {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("malformed response body: %v", err)}
	}
	if len(messages) == 0 {
		return nil
	}

	model, _ := response.Body["model"].(string)

	reqBody := map[string]any{
		"model":    model,
		"messages": messages,
	}
	payload, marshalErr := json.Marshal(reqBody)
	if marshalErr != nil {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("marshal request: %v", marshalErr)}
	}

	code, callErr := p.callNemoGuard(ctx, payload)
	if callErr != nil {
		if code == errcommon.Forbidden {
			return errcommon.Error{Code: code, Msg: "response blocked by NeMo guardrails"}
		}
		return errcommon.Error{Code: code, Msg: callErr.Error()}
	}
	return nil
}

// extractAssistantMessages extracts assistant content from a response body.
// It supports two payload formats:
// 1. OpenAI chat (via "choices"): choices (fail closed), and nil when no content is found.
// 2. MCP JSON-RPC: {"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"Hello"}]}}
//
// Returns (nil, nil) when no content is found.
func extractAssistantMessages(body map[string]any) ([]map[string]string, error) {
	if raw, ok := body["choices"]; ok {
		return extractOpenAIAssistantMessagesFromChoices(raw)
	}
	if _, ok := body["jsonrpc"]; ok {
		return extractMCPTextContent(body)
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
