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
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation/translator"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
)

// compile-time interface check
var _ translator.Translator = &VertexOpenAITranslator{}

// NewVertexOpenAITranslator creates a translator for Vertex AI's OpenAI-compatible endpoint.
// Path construction is handled by CRD path placeholders and applyPathOverride, not by the translator.
func NewVertexOpenAITranslator() *VertexOpenAITranslator {
	return &VertexOpenAITranslator{
		stripper: translator.NewResponseFieldStripper([]string{
			"usage.extra_properties",
		}),
	}
}

// VertexOpenAITranslator targets Vertex AI's native OpenAI-compatible chat/completions endpoint.
// Unlike VertexTranslator (which converts to Gemini generateContent format), this translator
// passes the request body through unchanged since the endpoint accepts OpenAI format natively.
// Path is set by applyPathOverride from CycleState, not by this translator.
type VertexOpenAITranslator struct {
	stripper *translator.ResponseFieldStripper
}

// TranslateRequest validates the request body. The request body is not mutated since
// the endpoint accepts OpenAI chat completions format as-is. 
func (t *VertexOpenAITranslator) TranslateRequest(body map[string]any) (translatedBody map[string]any,
	headersToMutate map[string]string, headersToRemove []string, err error) {
	model, _ := body["model"].(string)
	if model == "" {
		return nil, nil, nil, errcommon.Error{Code: errcommon.BadRequest, Msg: "model field is required"}
	}

	messages, _ := body["messages"].([]any)
	if len(messages) == 0 {
		return nil, nil, nil, errcommon.Error{Code: errcommon.BadRequest, Msg: "messages field is required and must not be empty"}
	}

	headersToMutate = map[string]string{
		"content-type": "application/json",
	}

	return nil, headersToMutate, nil, nil
}

// TranslateResponse strips Vertex AI-specific fields from the response.
// The endpoint returns standard OpenAI format with extra fields like usage.extra_properties
// (verified against aiplatform.googleapis.com/v1/.../chat/completions).
func (t *VertexOpenAITranslator) TranslateResponse(body map[string]any, model string) (translatedBody map[string]any, err error) {
	result, _ := t.stripper.Strip(body)
	return result, nil
}
