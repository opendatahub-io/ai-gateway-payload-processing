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
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/apikey-injection/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

// newTestPlugin creates an apiKeyInjectionPlugin for unit tests, bypassing the
// Handle-based Factory (which requires a real manager).
func newTestPlugin(store *secretStore) *ApiKeyInjectionPlugin {
	return &ApiKeyInjectionPlugin{
		typedName: plugin.TypedName{Type: APIKeyInjectionPluginType, Name: APIKeyInjectionPluginType},
		authHeadersGenerators: map[string]auth.AuthHeadersGenerator{
			"provider-with-prefix":    &auth.SimpleAuthGenerator{HeaderName: "Authorization", HeaderValuePrefix: "prefix "},
			"provider-without-prefix": &auth.SimpleAuthGenerator{HeaderName: "x-api-key"},
		},
		store: store,
	}
}

// newCycleState builds a CycleState with credential ref and optional provider.
func newCycleState(credsNamespace, credsName, providerName string) *framework.CycleState {
	cs := framework.NewCycleState()
	cs.Write(state.CredsRefName, credsName)
	cs.Write(state.CredsRefNamespace, credsNamespace)
	if providerName != "" {
		cs.Write(state.ProviderKey, providerName)
	}
	return cs
}

func TestProcessRequest(t *testing.T) {
	tests := []struct {
		name              string
		secrets           []*corev1.Secret
		request           *framework.InferenceRequest
		prepareCycleState func() *framework.CycleState
		wantHeaders       map[string]string
		errorContains     string
	}{
		{
			name:              "provider that has simple generator with prefix",
			secrets:           []*corev1.Secret{testSecret("default", "openai-key", "sk-test-key")},
			request:           framework.NewInferenceRequest(),
			prepareCycleState: func() *framework.CycleState { return newCycleState("default", "openai-key", "provider-with-prefix") },
			wantHeaders: map[string]string{
				"Authorization": "prefix sk-test-key",
			},
		},
		{
			name:    "provider that has simple generator without prefix",
			secrets: []*corev1.Secret{testSecret("default", "anthropic-key", "ant-key-123")},
			request: framework.NewInferenceRequest(),
			prepareCycleState: func() *framework.CycleState {
				return newCycleState("default", "anthropic-key", "provider-without-prefix")
			},
			wantHeaders: map[string]string{
				"x-api-key": "ant-key-123",
			},
		},
		{
			name:              "unknown provider — request fails",
			secrets:           []*corev1.Secret{testSecret("default", "no-provider", "sk-key")},
			request:           framework.NewInferenceRequest(),
			prepareCycleState: func() *framework.CycleState { return newCycleState("default", "no-provider", "some-unknown-provider") },
			errorContains:     "unsupported provider",
		},
		{
			name:              "internal model no provider - skip gracefully",
			secrets:           []*corev1.Secret{testSecret("default", "no-provider", "sk-key")},
			request:           framework.NewInferenceRequest(),
			prepareCycleState: func() *framework.CycleState { return framework.NewCycleState() },
			wantHeaders:       map[string]string{},
		},
		{
			name:    "missing credentials ref results in error",
			secrets: []*corev1.Secret{testSecret("default", "no-provider", "sk-key")},
			request: framework.NewInferenceRequest(),
			prepareCycleState: func() *framework.CycleState {
				cs := framework.NewCycleState()
				cs.Write(state.ProviderKey, "provider-with-prefix") // external model has provider but no creds
				return cs
			},
			errorContains: "missing credentialRef",
		},
		{
			name:    "secret not found results in error",
			secrets: []*corev1.Secret{},
			request: framework.NewInferenceRequest(),
			prepareCycleState: func() *framework.CycleState {
				return newCycleState("default", "unknown", "provider-with-prefix")
			},
			errorContains: "api key was not found",
		},
		{
			name:              "request is nil",
			secrets:           []*corev1.Secret{},
			request:           nil,
			prepareCycleState: func() *framework.CycleState { return framework.NewCycleState() },
			errorContains:     "request or headers is nil",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newSecretStore()
			for _, secret := range test.secrets {
				secretKey := fmt.Sprintf("%s/%s", secret.GetNamespace(), secret.GetName())
				require.NoError(t, store.addOrUpdate(secretKey, secret))
			}

			plugin := newTestPlugin(store)
			err := plugin.ProcessRequest(context.Background(), test.prepareCycleState(), test.request)
			if test.errorContains != "" {
				require.ErrorContains(t, err, test.errorContains)
				return
			}
			require.NoError(t, err)
			if diff := cmp.Diff(test.wantHeaders, test.request.Headers, cmpopts.SortMaps(func(a, b string) bool { return a < b }), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
