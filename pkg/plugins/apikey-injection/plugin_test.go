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

package apikey_injection

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"

	authgenerator "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/apikey-injection/auth-generator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

// newTestPlugin creates an apiKeyInjectionPlugin for unit tests, bypassing the
// Handle-based Factory (which requires a real manager).
func newTestPlugin(store *secretStore) *ApiKeyInjectionPlugin {
	return &ApiKeyInjectionPlugin{
		typedName: plugin.TypedName{Type: APIKeyInjectionPluginType, Name: APIKeyInjectionPluginType},
		authHeadersGenerators: map[auth.Auth]authgenerator.AuthHeadersGenerator{
			auth.APIKey: authgenerator.NewAPIKeyAuthGenerator(),
			auth.SigV4:  authgenerator.NewSigV4AuthGenerator(),
		},
		store: store,
	}
}

// newBedrockRequest creates an InferenceRequest pre-populated with a model body
// field and :path, simulating a real client request routed to Bedrock.
func newBedrockRequest() *requesthandling.InferenceRequest {
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "anthropic.claude-v2"
	req.Body["prompt"] = "hello"
	req.Headers[":path"] = "/default/bedrock-model/v1/chat/completions"
	return req
}

// newSigV4CycleState builds a CycleState with credential ref, sigv4 auth type, endpoint, and config.
func newSigV4CycleState(credsNamespace, credsName string) *plugin.CycleState {
	cs := newCycleState(credsNamespace, credsName, auth.SigV4)
	cs.Write(state.EndpointKey, "bedrock-runtime.us-east-1.amazonaws.com")
	cs.Write(state.ModelConfigKey, map[string]string{"service": "bedrock"})
	return cs
}

// newAPIKeyCycleState builds a CycleState with credential ref, apikey auth type,
// and config. The config simulates what the provider reconciler would inject
// (e.g. authHeaderName for providers like Anthropic/Azure).
func newAPIKeyCycleState(credsNamespace, credsName string, config map[string]string) *plugin.CycleState {
	cs := newCycleState(credsNamespace, credsName, auth.APIKey)
	cs.Write(state.ModelConfigKey, config)
	return cs
}

// newCycleState builds a CycleState with credential ref and auth type.
func newCycleState(credsNamespace, credsName string, authType auth.Auth) *plugin.CycleState {
	cs := plugin.NewCycleState()
	cs.Write(state.CredsRefName, credsName)
	cs.Write(state.CredsRefNamespace, credsNamespace)
	cs.Write(state.AuthTypeKey, authType)
	return cs
}

func TestProcessRequest(t *testing.T) {
	tests := []struct {
		name              string
		secrets           []*corev1.Secret
		prepareCycleState func() *plugin.CycleState
		wantHeaders       map[string]string
		errorContains     string
	}{
		{
			name:    "apikey auth with default Bearer prefix (OpenAI style)",
			secrets: []*corev1.Secret{testSecret("default", "openai-key", map[string]string{"api-key": "sk-test-key"})},
			prepareCycleState: func() *plugin.CycleState {
				return newAPIKeyCycleState("default", "openai-key", map[string]string{})
			},
			wantHeaders: map[string]string{
				"Authorization": "Bearer sk-test-key",
			},
		},
		{
			name:    "apikey auth with config-injected header (Anthropic style)",
			secrets: []*corev1.Secret{testSecret("default", "anthropic-key", map[string]string{"api-key": "ant-key-123"})},
			prepareCycleState: func() *plugin.CycleState {
				return newAPIKeyCycleState("default", "anthropic-key", map[string]string{auth.APIKeyAuthHeaderName: "x-api-key"})
			},
			wantHeaders: map[string]string{
				"x-api-key": "ant-key-123",
			},
		},
		{
			name:              "unknown auth type — request fails",
			secrets:           []*corev1.Secret{testSecret("default", "no-auth", map[string]string{"api-key": "sk-key"})},
			prepareCycleState: func() *plugin.CycleState { return newCycleState("default", "no-auth", "unknown-auth-type") },
			errorContains:     "unsupported authType",
		},
		{
			name:              "internal model no auth type - skip gracefully",
			secrets:           []*corev1.Secret{testSecret("default", "no-auth", map[string]string{"api-key": "sk-key"})},
			prepareCycleState: func() *plugin.CycleState { return plugin.NewCycleState() },
			wantHeaders:       map[string]string{},
		},
		{
			name:    "missing credentials ref results in error",
			secrets: []*corev1.Secret{testSecret("default", "no-creds", map[string]string{"api-key": "sk-key"})},
			prepareCycleState: func() *plugin.CycleState {
				cs := plugin.NewCycleState()
				cs.Write(state.AuthTypeKey, auth.APIKey) // external model has auth type but no creds
				return cs
			},
			errorContains: "missing credentialRef",
		},
		{
			name:    "credentials not found results in error",
			secrets: []*corev1.Secret{},
			prepareCycleState: func() *plugin.CycleState {
				return newAPIKeyCycleState("default", "unknown", map[string]string{})
			},
			errorContains: "credentials not found",
		},
		{
			name:    "missing api-key field in credentials results in error",
			secrets: []*corev1.Secret{testSecret("default", "wrong-fields", map[string]string{"wrong-field": "value"})},
			prepareCycleState: func() *plugin.CycleState {
				return newAPIKeyCycleState("default", "wrong-fields", map[string]string{})
			},
			errorContains: "failed to generate auth headers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newSecretStore()
			for _, secret := range test.secrets {
				require.NoError(t, store.addOrUpdate(secret.GetNamespace(), secret.GetName(), secret))
			}

			plugin := newTestPlugin(store)
			request := requesthandling.NewInferenceRequest()
			err := plugin.ProcessRequest(context.Background(), test.prepareCycleState(), request)
			if test.errorContains != "" {
				require.ErrorContains(t, err, test.errorContains)
				return
			}
			require.NoError(t, err)
			if diff := cmp.Diff(test.wantHeaders, request.Headers, cmpopts.SortMaps(func(a, b string) bool { return a < b }), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestProcessRequest_AWSBedrock(t *testing.T) {
	tests := []struct {
		name              string
		secrets           []*corev1.Secret
		prepareCycleState func() *plugin.CycleState
		wantSecurityToken string // exact value; empty means the header must be absent
		errorContains     string
	}{
		{
			name: "produces SigV4 auth headers",
			secrets: []*corev1.Secret{testSecret("default", "bedrock-creds", map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			})},
			prepareCycleState: func() *plugin.CycleState { return newSigV4CycleState("default", "bedrock-creds") },
		},
		{
			name: "includes security token when session token is present",
			secrets: []*corev1.Secret{testSecret("default", "bedrock-creds", map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"aws-session-token":     "FwoGZXIvYXdzEBYaDH7example-session-token",
			})},
			prepareCycleState: func() *plugin.CycleState { return newSigV4CycleState("default", "bedrock-creds") },
			wantSecurityToken: "FwoGZXIvYXdzEBYaDH7example-session-token",
		},
		{
			name: "uses explicit region from provider config map",
			secrets: []*corev1.Secret{testSecret("default", "bedrock-creds", map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			})},
			prepareCycleState: func() *plugin.CycleState {
				cs := newSigV4CycleState("default", "bedrock-creds")
				cs.Write(state.ModelConfigKey, map[string]string{"region": "ap-northeast-1", "service": "bedrock"})
				return cs
			},
		},
		{
			name: "missing endpoint in cycle state returns error",
			secrets: []*corev1.Secret{testSecret("default", "bedrock-creds", map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			})},
			prepareCycleState: func() *plugin.CycleState { return newCycleState("default", "bedrock-creds", auth.SigV4) },
			errorContains:     "failed to extract request data",
		},
		{
			name:              "missing aws credentials returns error",
			secrets:           []*corev1.Secret{testSecret("default", "bedrock-creds", map[string]string{"wrong-field": "value"})},
			prepareCycleState: func() *plugin.CycleState { return newSigV4CycleState("default", "bedrock-creds") },
			errorContains:     "failed to generate auth headers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newSecretStore()
			for _, secret := range test.secrets {
				require.NoError(t, store.addOrUpdate(secret.GetNamespace(), secret.GetName(), secret))
			}

			plugin := newTestPlugin(store)
			request := newBedrockRequest()
			err := plugin.ProcessRequest(context.Background(), test.prepareCycleState(), request)

			if test.errorContains != "" {
				require.ErrorContains(t, err, test.errorContains)
				return
			}
			require.NoError(t, err)

			// SigV4 Authorization is dynamic (timestamp, signature), so we verify the scheme prefix only.
			require.True(t, strings.HasPrefix(request.Headers["Authorization"], "AWS4-HMAC-SHA256"),
				"Authorization header should start with AWS4-HMAC-SHA256, got: %s", request.Headers["Authorization"])
			require.NotEmpty(t, request.Headers["X-Amz-Date"])
			require.NotEmpty(t, request.Headers["X-Amz-Content-Sha256"])

			if diff := cmp.Diff(test.wantSecurityToken, request.Headers["X-Amz-Security-Token"]); diff != "" {
				t.Errorf("X-Amz-Security-Token mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
