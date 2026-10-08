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
	"sort"
	"strings"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

const (
	// NemoRequestGuardPluginType is the plugin type identifier.
	NemoRequestGuardPluginType = "nemo-request-guard"
)

// compile-time type validation
var _ requesthandling.RequestProcessor = &NemoRequestGuardPlugin{}

// NemoRequestGuardPlugin calls a NeMo Guardrails service over HTTP to check request content
// using input rails. It implements RequestProcessor to intercept requests before forwarding.
type NemoRequestGuardPlugin struct {
	typedName plugin.TypedName
	nemoGuardBase
}

// NemoRequestGuardFactory is the factory function for NemoRequestGuardPlugin.
func NemoRequestGuardFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	config := nemoGuardConfig{
		TimeoutSeconds: defaultTimeoutSec,
	}

	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &config); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of '%s' plugin - %w", NemoRequestGuardPluginType, err)
		}
	}

	plugin, err := NewNemoRequestGuardPlugin(config.NemoURL, config.TimeoutSeconds)
	if err != nil {
		return nil, fmt.Errorf("failed to create '%s' plugin - %w", NemoRequestGuardPluginType, err)
	}

	return plugin.WithName(name), nil
}

// NewNemoRequestGuardPlugin builds a NeMo request guard plugin from validated parameters.
// The NeMo server is expected to have a default configuration (--default-config-id).
func NewNemoRequestGuardPlugin(nemoURL string, timeoutSeconds int) (*NemoRequestGuardPlugin, error) {
	base, err := newNemoGuardBase(nemoURL, timeoutSeconds)
	if err != nil {
		return nil, err
	}
	return &NemoRequestGuardPlugin{
		typedName:     plugin.TypedName{Type: NemoRequestGuardPluginType, Name: NemoRequestGuardPluginType},
		nemoGuardBase: *base,
	}, nil
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *NemoRequestGuardPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the name of the plugin instance.
func (p *NemoRequestGuardPlugin) WithName(name string) *NemoRequestGuardPlugin {
	p.typedName.Name = name
	return p
}

// ProcessRequest calls NeMo Guardrails to evaluate input rails on the incoming request.
// It extracts user-supplied text from either an OpenAI-style chat body (via "messages")
// or an MCP JSON-RPC body (via "params.arguments"), POSTs to the configured NeMo
// endpoint, and returns an errcommon.Error with Forbidden (403) if NeMo blocks the
// content.
//
// NeMo always returns HTTP 200 for both allowed and blocked requests. The decision is
// conveyed through the response body "status" field: "passed" means the request passed
// all rails, "modified" means content was redacted (currently passed through as-is),
// and "blocked" means the request is blocked.
func (p *NemoRequestGuardPlugin) ProcessRequest(ctx context.Context, _ *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	model, ok := request.Body["model"].(string)
	if !ok {
		model = ""
	}

	messages, err := extractMessages(request.Body)
	if err != nil {
		return errcommon.Error{Code: errcommon.BadRequest, Msg: fmt.Sprintf("malformed request body: %v", err)}
	}
	if len(messages) == 0 {
		return nil // no messages to check (e.g. non-chat request) → allow
	}

	// "model" field is required by the NeMo OpenAI-compatible API schema but is not used.
	// the guard model is defined in NeMo's config.yml.
	reqBody := map[string]any{
		"model":    model,
		"messages": messages,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("marshal request: %v", err)}
	}

	code, callErr := p.callNemoGuard(ctx, payload)
	if callErr != nil {
		if code == errcommon.Forbidden {
			return errcommon.Error{Code: code, Msg: "request blocked by NeMo guardrails"}
		}
		return errcommon.Error{Code: code, Msg: callErr.Error()}
	}
	return nil
}

// extractMessages returns user-supplied text as a message slice suitable for NeMo's
// OpenAI-compatible chat endpoint. It supports two payload formats:
//
//  1. OpenAI chat: top-level "messages" array → forwards all messages.
//  2. MCP JSON-RPC: {"jsonrpc":"2.0","params":{"arguments":{…}}} → concatenates
//     all string argument values into a single user message.
//
// Returns (nil, nil) when no content is found.
func extractMessages(body map[string]any) ([]any, error) {
	if raw, ok := body["messages"]; ok {
		return extractOpenAIMessages(raw)
	}
	if _, ok := body["jsonrpc"]; ok {
		return extractMCPArguments(body)
	}
	return nil, nil // not an inference request (e.g. API key management, model listing)
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
