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

package awsbedrock

import (
	"fmt"
)

const (
	// Bedrock OpenAI-compatible endpoint path
	bedrockOpenAIPath = "/v1/chat/completions"
)

// BedrockApiType represents the type of Bedrock API to use
type BedrockApiType string

const (
	ConverseApi BedrockApiType = "ConverseApi"
	InvokeApi   BedrockApiType = "InvokeApi"
	OpenAiApi   BedrockApiType = "OpenAiApi"
)

// BedrockTranslator translates OpenAI Chat Completions to AWS Bedrock's OpenAI-compatible API
// This is a simple path rewriter since Bedrock's OpenAI-compatible endpoint uses the same format
type BedrockTranslator struct {
	apiType BedrockApiType
}

// NewBedrockTranslator creates a new AWS Bedrock translator instance
func NewBedrockTranslator() *BedrockTranslator {
	return &BedrockTranslator{
		// Makes it clear that only Bedrock's OpenAi api endpoint is supported currently.
		// Placeholder to set other Bedrock api types if supported in future.
		apiType: OpenAiApi,
	}
}

// TranslateRequest rewrites the path to target Bedrock's OpenAI-compatible endpoint.
// The request body is not mutated since Bedrock's OpenAI-compatible API accepts the same schema as OpenAI.
func (t *BedrockTranslator) TranslateRequest(body map[string]any) (
	translatedBody map[string]any,
	headersToMutate map[string]string,
	headersToRemove []string,
	err error,
) {
	// Validate required fields
	model, ok := body["model"].(string)
	if !ok || model == "" {
		return nil, nil, nil, fmt.Errorf("model field is required")
	}

	// Validate that this is a chat completions request with proper messages
	messagesRaw, hasMessages := body["messages"]
	if !hasMessages {
		return nil, nil, nil, fmt.Errorf("messages field is required for chat completions")
	}

	messages, ok := messagesRaw.([]interface{})
	if !ok {
		return nil, nil, nil, fmt.Errorf("messages field must be an array")
	}

	if len(messages) == 0 {
		return nil, nil, nil, fmt.Errorf("messages array cannot be empty")
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
func (t *BedrockTranslator) TranslateResponse(body map[string]any, model string) (
	translatedBody map[string]any,
	err error,
) {
	// No translation needed - Bedrock's OpenAI-compatible endpoint returns OpenAI format
	return nil, nil
}
