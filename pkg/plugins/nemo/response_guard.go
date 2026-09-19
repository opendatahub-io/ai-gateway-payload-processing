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

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	// NemoResponseGuardPluginType is the plugin type identifier.
	NemoResponseGuardPluginType = "nemo-response-guard"
)

// compile-time type validation
var _ requesthandling.ResponseProcessor = &NemoResponseGuardPlugin{}

// NemoResponseGuardPlugin calls a NeMo Guardrails service over HTTP to check model output
// using output rails. It implements ResponseProcessor to inspect responses before returning
// them to the caller.
type NemoResponseGuardPlugin struct {
	typedName plugin.TypedName
	nemoGuardBase
}

// NemoResponseGuardFactory is the factory function for NemoResponseGuardPlugin.
func NemoResponseGuardFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
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
// It extracts assistant messages from the OpenAI-style response body, POSTs them to the
// configured NeMo endpoint, and returns an errcommon.Error with Forbidden (403) if NeMo
// blocks the content.
//
// NeMo always returns HTTP 200 for both allowed and blocked responses. The decision is
// conveyed through the response body "status" field: "passed" means the response passed
// all rails, "modified" means content was redacted (currently passed through as-is),
// and "blocked" means the response is blocked.
func (p *NemoResponseGuardPlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	inputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.InputAPIFormatKey)
	messages, err := extractAssistantMessages(response.Body, inputFormat)
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
