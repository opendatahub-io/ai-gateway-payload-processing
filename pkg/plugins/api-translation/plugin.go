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
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/anthropic"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/azure"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/bedrock"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/openai"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator/vertex"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	APITranslationPluginType = "api-translation"
)

// compile-time type validation
var _ framework.RequestProcessor = &APITranslationPlugin{}
var _ framework.ResponseProcessor = &APITranslationPlugin{}

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
func APITranslationFactory(name string, rawConfig json.RawMessage, handle framework.Handle) (framework.BBRPlugin, error) {
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
func (p *APITranslationPlugin) ProcessRequest(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest) error {
	providerName, err := framework.ReadCycleStateKey[string](cycleState, state.ProviderKey) // err if not found
	if err != nil || providerName == "" {                                                   // empty provider means no translation needed
		return nil
	}

	translator, ok := p.providers[providerName]
	if !ok {
		return fmt.Errorf("unsupported provider - '%s'", providerName)
	}

	translatedBody, headersToMutate, headersToRemove, err := translator.TranslateRequest(request.Body)
	if err != nil {
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

	// content-length is another special header that will be set automatically by the pluggable framework when the body is mutated.

	return nil
}

// ProcessResponse reads the provider from CycleState and translates the response
// back to OpenAI Chat Completions format if needed.
func (p *APITranslationPlugin) ProcessResponse(ctx context.Context, cycleState *framework.CycleState, response *framework.InferenceResponse) error {
	providerName, err := framework.ReadCycleStateKey[string](cycleState, state.ProviderKey) // err if not found
	if err != nil || providerName == "" {                                                   // empty provider means no translation needed
		return nil
	}

	translator, ok := p.providers[providerName]
	if !ok {
		return fmt.Errorf("unsupported provider - '%s'", providerName)
	}

	model, _ := framework.ReadCycleStateKey[string](cycleState, state.ModelKey)

	translatedBody, err := translator.TranslateResponse(response.Body, model)
	if err != nil {
		var commErr errcommon.Error
		if errors.As(err, &commErr) {
			return commErr
		}
		return fmt.Errorf("response translation failed for provider '%s' - %w", providerName, err)
	}

	if translatedBody != nil {
		response.SetBody(translatedBody)
	}

	return nil
}
