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

package authgenerator

import (
	"fmt"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
)

// apiKeyField is the field name in the credentials map that holds the API key.
const (
	apiKeyField            = "api-key"
	defaultAuthHeader      = "Authorization"
	defaultAuthValuePrefix = "Bearer "
)

// compile-time interface check
var _ AuthHeadersGenerator = &SimpleAuthGenerator{}

func NewSimpleAuthGenerator() *SimpleAuthGenerator {
	return &SimpleAuthGenerator{}
}

// SimpleAuthGenerator generates a single auth header from an API key.
// ExtractRequestData resolves the header name and value prefix from the model
// config in CycleState (defaults are injected by the provider reconciler).
// GenerateAuthHeaders reads those values from the merged credentials map.
// Requires the credentials map to contain an "api-key" field.
type SimpleAuthGenerator struct{}

// ExtractRequestData resolves the auth header name and value prefix from the
// model config in CycleState. Provider-specific defaults (e.g. "x-api-key" for
// Anthropic) are injected into the config by the provider reconciler. If not
// set, falls back to "Authorization" with "Bearer " prefix.
func (g *SimpleAuthGenerator) ExtractRequestData(cycleState *framework.CycleState, _ *framework.InferenceRequest) (map[string]string, error) {
	config, err := framework.ReadCycleStateKey[map[string]string](cycleState, state.ModelConfigKey)
	if err != nil {
		return nil, fmt.Errorf("failed to extract config from cycle state - %w", err)
	}

	authHeader := defaultAuthHeader
	authValuePrefix := defaultAuthValuePrefix

	if headerName, ok := config[auth.SimpleAuthHeaderName]; ok && headerName != "" {
		authHeader = headerName
		authValuePrefix = ""                                           // non-default header implies no prefix unless explicitly set
		if valuePrefix, ok := config[auth.SimpleAuthValuePrefix]; ok { // valuePrefix can be set only if header name is set
			authValuePrefix = valuePrefix
		}
	}

	return map[string]string{
		auth.SimpleAuthHeaderName:  authHeader,
		auth.SimpleAuthValuePrefix: authValuePrefix,
	}, nil
}

// GenerateAuthHeaders extracts the relevant fields from credentialsData and returns
// the header name and formatted value. Returns an error if the field is missing.
func (g *SimpleAuthGenerator) GenerateAuthHeaders(credentialsData map[string]string) (map[string]string, error) {
	apiKey, ok := credentialsData[apiKeyField]
	if !ok {
		return nil, fmt.Errorf("credentials missing required field %s", apiKeyField)
	}

	headerName, ok := credentialsData[auth.SimpleAuthHeaderName]
	if !ok {
		return nil, fmt.Errorf("credentials missing required field %s", auth.SimpleAuthHeaderName)
	}

	valuePrefix, ok := credentialsData[auth.SimpleAuthValuePrefix]
	if !ok {
		return nil, fmt.Errorf("credentials missing required field %s", auth.SimpleAuthValuePrefix)
	}

	return map[string]string{
		headerName: fmt.Sprintf("%s%s", valuePrefix, apiKey),
	}, nil
}
