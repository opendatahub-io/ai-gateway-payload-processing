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

package openai

import (
	"fmt"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
)

const (
	// OpenAI-compatible endpoint path
	openAIPath = "/v1/chat/completions"
)

// compile-time interface check
var _ translator.Translator = &OpenAITranslator{}

// NewAzureOpenAITranslator initializes a new OpenAITranslator and returns its pointer.
func NewOpenAITranslator() *OpenAITranslator {
	return &OpenAITranslator{}
}

// OpenAITranslator only set the relative path.
// this is needed in case the original request uses different relative path.
type OpenAITranslator struct{}

// TranslateRequest rewrites the path and headers for OpenAI.
func (t *OpenAITranslator) TranslateRequest(body map[string]any) (map[string]any, map[string]string, []string, error) {
	model, _ := body["model"].(string)
	if model == "" {
		return nil, nil, nil, fmt.Errorf("model field is required")
	}

	headers := map[string]string{
		":path": openAIPath, // needed in case the original request uses different relative path and include the model.
	}

	// Return nil body — no body mutation is needed, Azure accepts the OpenAI request format as-is.
	return nil, headers, nil, nil
}

// TranslateResponse is a no-op.
func (t *OpenAITranslator) TranslateResponse(body map[string]any, model string) (map[string]any, error) {
	return nil, nil
}
