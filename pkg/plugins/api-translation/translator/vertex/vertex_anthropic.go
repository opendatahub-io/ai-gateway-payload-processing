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
	"fmt"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/anthropic"
)

const (
	// AnthropicVersionConfigKey is the key in the ExternalModel/Provider config map
	// that carries the Anthropic API version for Vertex AI Claude requests.
	AnthropicVersionConfigKey = "anthropicVersion"

	// anthropicBetaHeader carries client-requested Anthropic beta features.
	// Vertex AI rejects the whole request (400) when the header contains any
	// beta flag it does not recognize, so vertex translators strip the header.
	anthropicBetaHeader = "anthropic-beta"
)

// vertexUnsupportedBodyFields are Anthropic Messages API request fields that Vertex AI's
// Claude endpoint rejects with 400 "Extra inputs are not permitted". Clients such as
// Claude Code send them; the corresponding features degrade gracefully when dropped.
var vertexUnsupportedBodyFields = []string{
	"context_management",
	"betas",
	"mcp_servers",
	"service_tier",
	"container",
}

func stripVertexUnsupportedFields(body map[string]any) {
	for _, f := range vertexUnsupportedBodyFields {
		delete(body, f)
	}
}

// compile-time interface checks
var _ translator.Translator = &VertexAnthropicTranslator{}
var _ translator.ConfigAwareTranslator = &VertexAnthropicTranslator{}

// NewVertexAnthropicTranslator creates a translator for Vertex AI's Anthropic Claude endpoint.
// Path construction is handled by CRD path placeholders and applyPathOverride, not by the translator.
func NewVertexAnthropicTranslator() *VertexAnthropicTranslator {
	return &VertexAnthropicTranslator{
		anthropic: anthropic.NewAnthropicTranslator(),
	}
}

// VertexAnthropicTranslator translates OpenAI Chat Completions to Anthropic Messages API
// format for Claude models on Google Cloud Vertex AI. Body transformation reuses
// AnthropicTranslator; Vertex-specific differences are anthropic_version in the body
// (not header) and model removed from body (path is set by applyPathOverride from CRD config).
type VertexAnthropicTranslator struct {
	anthropic *anthropic.AnthropicTranslator
}

// TranslateRequest satisfies the base Translator interface but should not be called directly.
// Use TranslateRequestWithConfig instead; the plugin dispatches via ConfigAwareTranslator.
func (t *VertexAnthropicTranslator) TranslateRequest(body map[string]any) (map[string]any, map[string]string, []string, error) {
	return nil, nil, nil, fmt.Errorf("vertex-anthropic requires config; use TranslateRequestWithConfig")
}

// TranslateRequestWithConfig delegates to AnthropicTranslator for body transformation, then
// applies Vertex-specific adjustments: removes model from body (path is set by applyPathOverride),
// injects anthropic_version from config, removes anthropic-version header.
func (t *VertexAnthropicTranslator) TranslateRequestWithConfig(body map[string]any, config map[string]string) (map[string]any, map[string]string, []string, error) {
	translatedBody, _, headersToRemove, err := t.anthropic.TranslateRequest(body)
	if err != nil {
		return nil, nil, nil, err
	}

	delete(translatedBody, "model")

	anthropicVersion := config[AnthropicVersionConfigKey]
	if anthropicVersion == "" {
		return nil, nil, nil, fmt.Errorf("%s is required in ExternalModel/Provider config for vertex-anthropic", AnthropicVersionConfigKey)
	}
	translatedBody["anthropic_version"] = anthropicVersion
	stripVertexUnsupportedFields(translatedBody)

	headers := map[string]string{
		"content-type": "application/json",
	}

	return translatedBody, headers, append(headersToRemove, anthropicBetaHeader), nil
}

// TranslateResponse delegates to AnthropicTranslator since Vertex returns the same
// Anthropic Messages API response format.
func (t *VertexAnthropicTranslator) TranslateResponse(body map[string]any, model string) (map[string]any, error) {
	return t.anthropic.TranslateResponse(body, model)
}

// VertexAnthropicPassthroughTranslator handles native Anthropic Messages API clients
// (e.g., Claude Code) targeting Vertex AI Claude. The body is already in Anthropic
// format so no conversion is needed — just inject anthropic_version and remove model.
type VertexAnthropicPassthroughTranslator struct{}

var _ translator.Translator = &VertexAnthropicPassthroughTranslator{}
var _ translator.ConfigAwareTranslator = &VertexAnthropicPassthroughTranslator{}

func NewVertexAnthropicPassthroughTranslator() *VertexAnthropicPassthroughTranslator {
	return &VertexAnthropicPassthroughTranslator{}
}

func (t *VertexAnthropicPassthroughTranslator) TranslateRequest(body map[string]any) (map[string]any, map[string]string, []string, error) {
	return nil, nil, nil, fmt.Errorf("vertex-anthropic-passthrough requires config; use TranslateRequestWithConfig")
}

func (t *VertexAnthropicPassthroughTranslator) TranslateRequestWithConfig(body map[string]any, config map[string]string) (map[string]any, map[string]string, []string, error) {
	anthropicVersion := config[AnthropicVersionConfigKey]
	if anthropicVersion == "" {
		return nil, nil, nil, fmt.Errorf("%s is required in ExternalModel/Provider config for vertex-anthropic", AnthropicVersionConfigKey)
	}
	body["anthropic_version"] = anthropicVersion
	delete(body, "model")
	stripVertexUnsupportedFields(body)

	headers := map[string]string{"content-type": "application/json"}
	return body, headers, []string{anthropicBetaHeader}, nil
}

func (t *VertexAnthropicPassthroughTranslator) TranslateResponse(body map[string]any, _ string) (map[string]any, error) {
	return nil, nil
}
