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
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/anthropic"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/azure"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/bedrock"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/openai"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/vertex"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	APITranslationPluginType = "api-translation"
)

// compile-time type validation
var _ requesthandling.RequestProcessor = &APITranslationPlugin{}
var _ requesthandling.ResponseProcessor = &APITranslationPlugin{}

// apiTranslationConfig holds configuration for provider-specific translators.
type apiTranslationConfig struct {
	VertexOpenAI *vertexOpenAIConfig `json:"vertexOpenAI,omitempty"`
}

type vertexOpenAIConfig struct {
	Project  string `json:"project"`
	Location string `json:"location"`
	Endpoint string `json:"endpoint"`
}

// APITranslationFactory defines the factory function for APITranslationPlugin.
func APITranslationFactory(name string, rawConfig json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	var config apiTranslationConfig
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &config); err != nil {
			return nil, fmt.Errorf("failed to parse api-translation plugin config: %w", err)
		}
	}

	p, err := NewAPITranslationPlugin(handle.Context(), config)
	if err != nil {
		return nil, err
	}
	return p.WithName(name), nil
}

// NewAPITranslationPlugin creates a new plugin instance with the given config.
// If vertexOpenAI config is provided, the vertex-openai translator is registered.
// If vertexOpenAI config is provided but has empty fields, an error is returned.
func NewAPITranslationPlugin(ctx context.Context, config apiTranslationConfig) (*APITranslationPlugin, error) {
	// vertex (native GenerateContent) is not used in 3.4 ExternalModel flow.
	// Uncomment when vertex (non-OpenAI) provider support is needed.
	// vertexTranslator := vertex.NewVertexTranslator()
	providers := map[string]translator.Translator{
		provider.OpenAI:        openai.NewOpenAITranslator(),
		provider.Anthropic:     anthropic.NewAnthropicTranslator(),
		provider.AzureOpenAI:   azure.NewAzureOpenAITranslator(),
		provider.BedrockOpenAI: bedrock.NewBedrockOpenAITranslator(),
		provider.RemoteMaaS:    openai.NewOpenAITranslator(),
	}

	if config.VertexOpenAI != nil {
		if config.VertexOpenAI.Project == "" || config.VertexOpenAI.Location == "" || config.VertexOpenAI.Endpoint == "" {
			return nil, fmt.Errorf("vertexOpenAI config requires non-empty project, location, and endpoint")
		}
		providers[provider.VertexOpenAI] = vertex.NewVertexOpenAITranslator(
			config.VertexOpenAI.Project,
			config.VertexOpenAI.Location,
			config.VertexOpenAI.Endpoint,
		)
	}

	keys := make([]string, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}

	log.FromContext(ctx).V(logutil.VERBOSE).Info("plugin initialized", "providers", strings.Join(keys, ","))

	return &APITranslationPlugin{
		typedName: plugin.TypedName{
			Type: APITranslationPluginType,
			Name: APITranslationPluginType,
		},
		providers: providers,
	}, nil
}

// APITranslationPlugin translates inference API requests and responses between
// OpenAI Chat Completions format and provider-native formats (e.g., Anthropic Messages API).
type APITranslationPlugin struct {
	typedName plugin.TypedName
	providers map[string]translator.Translator // map from provider name to translator interface
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

// ProcessRequest reads the provider from CycleState (set by an upstream plugin) and translates
// the request body from OpenAI format to the provider's native format if needed.
// When the incoming client format matches the upstream API format (passthrough mode),
// translation is skipped entirely.
func (p *APITranslationPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	providerName, err := plugin.ReadCycleStateKey[string](cycleState, state.ProviderKey) // err if not found
	if err != nil || providerName == "" {                                                   // empty provider means no translation needed
		return nil
	}

	if isPassthrough(cycleState) {
		logger.Info("passthrough mode — skipping request translation")
		// Remove client auth header; apikey-injection plugin adds the provider credential downstream.
		request.RemoveHeader("authorization")
		// Override :path if a custom path is configured in CycleState (cross-cluster routing).
		if pathOverride, err := plugin.ReadCycleStateKey[string](cycleState, state.PathKey); err == nil && pathOverride != "" {
			request.SetHeader(":path", pathOverride)
		}
		return nil
	}

	translator, ok := p.providers[providerName]
	if !ok {
		logger.Error(nil, "unsupported provider for translation", "provider", providerName)
		return fmt.Errorf("unsupported provider - '%s'", providerName)
	}

	translatedBody, headersToMutate, headersToRemove, err := translator.TranslateRequest(request.Body)
	if err != nil {
		logger.Error(err, "request translation failed", "provider", providerName)
		var commErr errcommon.Error
		if errors.As(err, &commErr) {
			return commErr
		}
		return fmt.Errorf("request translation failed for provider '%s' - %w", providerName, err)
	}

	if translatedBody != nil {
		request.SetBody(translatedBody)
	}

	for key, value := range headersToMutate {
		request.SetHeader(key, value)
	}
	for _, key := range headersToRemove {
		request.RemoveHeader(key)
	}

	// authorization is a special header removed by the plugin, no matter which provider is used.
	// The api-key is expected to be set by the the api-key injection plugin.
	request.RemoveHeader("authorization")

	// Override :path if a custom path is configured in CycleState (cross-cluster routing).
	if pathOverride, err := plugin.ReadCycleStateKey[string](cycleState, state.PathKey); err == nil && pathOverride != "" {
		request.SetHeader(":path", pathOverride)
	}

	// content-length is another special header that will be set automatically by the pluggable framework when the body is mutated.

	logger.Info("request api-translation completed successfully", "provider", providerName)
	return nil
}

// ProcessResponse reads the provider from CycleState and translates the response
// back to OpenAI Chat Completions format if needed.
// When in passthrough mode, translation is skipped.
func (p *APITranslationPlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	providerName, err := plugin.ReadCycleStateKey[string](cycleState, state.ProviderKey) // err if not found
	if err != nil || providerName == "" {                                                   // empty provider means no translation needed
		return nil
	}

	if isPassthrough(cycleState) {
		logger.Info("passthrough mode — skipping response translation")
		return nil
	}

	translator, ok := p.providers[providerName]
	if !ok {
		logger.Error(nil, "unsupported provider for response translation", "provider", providerName)
		return fmt.Errorf("unsupported provider - '%s'", providerName)
	}

	model, _ := plugin.ReadCycleStateKey[string](cycleState, state.ModelKey)

	translatedBody, err := translator.TranslateResponse(response.Body, model)
	if err != nil {
		logger.Error(err, "response translation failed", "provider", providerName)
		var commErr errcommon.Error
		if errors.As(err, &commErr) {
			return commErr
		}
		return fmt.Errorf("response translation failed for provider '%s' - %w", providerName, err)
	}

	if translatedBody != nil {
		response.SetBody(translatedBody)
	}

	logger.Info("response api-translation completed successfully", "provider", providerName)
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
