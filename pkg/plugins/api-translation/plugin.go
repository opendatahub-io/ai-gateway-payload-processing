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

package api_translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/anthropic"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/openai"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	APITranslationPluginType = "api-translation"
)

// compile-time type validation
var _ requesthandling.RequestProcessor = &APITranslationPlugin{}
var _ requesthandling.ResponseProcessor = &APITranslationPlugin{}

// APITranslationFactory defines the factory function for APITranslationPlugin.
func APITranslationFactory(name string, _ json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	p := NewAPITranslationPlugin(handle.Context())
	return p.WithName(name), nil
}

// translatorKey identifies a translator by the combination of input and output API formats.
type translatorKey struct {
	input  apiformat.APIFormat
	output apiformat.APIFormat
}

// NewAPITranslationPlugin creates a new plugin instance with all translators registered.
// Provider-specific configuration (e.g., Vertex AI project/location/endpoint) is read
// from ExternalProvider CRD config and path placeholders at runtime, not from plugin config.
func NewAPITranslationPlugin(ctx context.Context) *APITranslationPlugin {
	translators := map[translatorKey]translator.Translator{
		{apiformat.OpenAIChatCompletions, apiformat.OpenAIChatCompletions}: openai.NewOpenAITranslator(),
		{apiformat.OpenAIChatCompletions, apiformat.Messages}:             anthropic.NewAnthropicTranslator(),
	}

	log.FromContext(ctx).V(logutil.VERBOSE).Info("plugin initialized", "translators", len(translators))

	return &APITranslationPlugin{
		typedName: plugin.TypedName{
			Type: APITranslationPluginType,
			Name: APITranslationPluginType,
		},
		translators: translators,
	}
}

// APITranslationPlugin translates inference API requests and responses between
// API formats based on the (inputFormat, outputFormat) tuple from CycleState.
type APITranslationPlugin struct {
	typedName   plugin.TypedName
	translators map[translatorKey]translator.Translator
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *APITranslationPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the name of the plugin instance.
func (p *APITranslationPlugin) WithName(name string) *APITranslationPlugin {
	p.typedName.Name = name
	return p
}

// applyPathOverride sets the :path pseudo-header from CycleState.
// Path is a required field on ExternalProviderRef (MinLength=1), so it is always present.
func applyPathOverride(cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) {
	if path, err := plugin.ReadCycleStateKey[string](cycleState, state.PathKey); err == nil {
		request.SetHeader(":path", path)
	}
}

// ProcessRequest reads the provider from CycleState (set by an upstream plugin) and translates
// the request body from OpenAI format to the provider's native format if needed.
// When the incoming client format matches the upstream API format (passthrough mode),
// translation is skipped entirely.
func (p *APITranslationPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	inputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.InputAPIFormatKey)
	outputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.APIFormatKey)
	if inputFormat == "" || outputFormat == "" {
		return nil
	}

	if isPassthrough(cycleState) {
		logger.Info("passthrough mode — skipping request translation")
		request.RemoveHeader("authorization")
		applyPathOverride(cycleState, request)
		return nil
	}

	key := translatorKey{input: inputFormat, output: outputFormat}
	translator, ok := p.translators[key]
	if !ok {
		logger.Error(nil, "unsupported format combination for translation", "input", inputFormat, "output", outputFormat)
		return fmt.Errorf("unsupported format combination: %s → %s", inputFormat, outputFormat)
	}

	translatedBody, headersToMutate, headersToRemove, err := translator.TranslateRequest(request.Body)
	if err != nil {
		logger.Error(err, "request translation failed", "input", inputFormat, "output", outputFormat)
		var commErr errcommon.Error
		if errors.As(err, &commErr) {
			return commErr
		}
		return fmt.Errorf("request translation failed for %s → %s: %w", inputFormat, outputFormat, err)
	}

	if translatedBody != nil {
		request.SetBody(translatedBody)
	}

	for k, v := range headersToMutate {
		request.SetHeader(k, v)
	}
	for _, k := range headersToRemove {
		request.RemoveHeader(k)
	}

	request.RemoveHeader("authorization")

	applyPathOverride(cycleState, request)

	logger.Info("request api-translation completed successfully", "input", inputFormat, "output", outputFormat)
	return nil
}

// ProcessResponse reads format info from CycleState and translates the response
// back to the client's input format if needed.
func (p *APITranslationPlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	inputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.InputAPIFormatKey)
	outputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.APIFormatKey)
	if inputFormat == "" || outputFormat == "" {
		return nil
	}

	if isPassthrough(cycleState) {
		logger.Info("passthrough mode — skipping response translation")
		return nil
	}

	key := translatorKey{input: inputFormat, output: outputFormat}
	translator, ok := p.translators[key]
	if !ok {
		logger.Error(nil, "unsupported format combination for response translation", "input", inputFormat, "output", outputFormat)
		return fmt.Errorf("unsupported format combination: %s → %s", inputFormat, outputFormat)
	}

	model, _ := plugin.ReadCycleStateKey[string](cycleState, state.ModelKey)

	translatedBody, err := translator.TranslateResponse(response.Body, model)
	if err != nil {
		logger.Error(err, "response translation failed", "input", inputFormat, "output", outputFormat)
		var commErr errcommon.Error
		if errors.As(err, &commErr) {
			return commErr
		}
		return fmt.Errorf("response translation failed for %s → %s: %w", inputFormat, outputFormat, err)
	}

	if translatedBody != nil {
		response.SetBody(translatedBody)
	}

	logger.Info("response api-translation completed successfully", "input", inputFormat, "output", outputFormat)
	return nil
}

// isPassthrough checks whether the request should bypass translation.
// Passthrough activates when:
//  1. Both input and output API format keys are set in CycleState
//  2. They match (client speaks the same format as the upstream provider)
//  3. The format is NOT "openai-chat" — the OpenAI translator performs essential
//     :path rewriting (strips model prefix path) that is needed even when both
//     sides speak OpenAI format. Without this rewrite, the upstream provider would
//     receive the full MaaS-prefixed path (e.g., /llm/model/v1/chat/completions).
//
// This design is intentionally generic: adding support for new API formats
// (embeddings, audio, images) requires only adding a path mapping in
// detectInputAPIFormat — no changes needed here.
func isPassthrough(cycleState *plugin.CycleState) bool {
	inputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.InputAPIFormatKey)
	outputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.APIFormatKey)
	if inputFormat == "" || outputFormat == "" {
		return false
	}
	if inputFormat != outputFormat {
		return false
	}
	// OpenAI chat is excluded because the translator rewrites :path to strip the model prefix.
	return inputFormat != apiformat.OpenAIChatCompletions
}
