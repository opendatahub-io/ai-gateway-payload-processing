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

package bedrock

import (
	"fmt"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
)

const (
	// Bedrock OpenAI-compatible endpoint path
	bedrockOpenAIPath = "/openai/v1/chat/completions"
)

// compile-time interface check
var _ translator.Translator = &BedrockOpenAITranslator{}

// NewBedrockOpenAITranslator initializes a new BedrockOpenAITranslator and returns its pointer.
func NewBedrockOpenAITranslator() *BedrockOpenAITranslator {
	return &BedrockOpenAITranslator{}
}

// BedrockOpenAITranslator translates OpenAI Chat Completions to AWS Bedrock's OpenAI-compatible API
// This is a simple path rewriter since Bedrock's OpenAI-compatible endpoint uses the same format
type BedrockOpenAITranslator struct{}

// TranslateRequest rewrites the path to target Bedrock's OpenAI-compatible endpoint.
// The request body is not mutated since Bedrock's OpenAI-compatible API accepts the same schema as OpenAI.
func (t *BedrockOpenAITranslator) TranslateRequest(body map[string]any) (translatedBody map[string]any,
	headersToMutate map[string]string, headersToRemove []string, err error) {
	// Validate required fields
	model, ok := body["model"].(string)
	if !ok || model == "" {
		return nil, nil, nil, fmt.Errorf("model field is required")
	}

	// Build headers for Bedrock OpenAI-compatible endpoint
	headersToMutate = map[string]string{
		":path":        bedrockOpenAIPath,
		"content-type": "application/json",
	}

	// Return nil body — no body mutation needed, Bedrock accepts OpenAI request format as-is
	return nil, headersToMutate, nil, nil
}

// TranslateResponse is a no-op since Bedrock's OpenAI-compatible API returns responses in OpenAI format
func (t *BedrockOpenAITranslator) TranslateResponse(body map[string]any, model string) (translatedBody map[string]any, err error) {
	// No translation needed - Bedrock's OpenAI-compatible endpoint returns OpenAI format
	return nil, nil
}
