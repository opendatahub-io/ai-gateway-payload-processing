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
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/apikey-injection/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	// APIKeyInjectionPluginType is the registered name for this plugin in the BBR registry.
	APIKeyInjectionPluginType = "apikey-injection"
)

// compile-time interface check
var _ framework.RequestProcessor = &ApiKeyInjectionPlugin{}

// APIKeyInjectionFactory defines the factory function for ApiKeyInjectionPlugin.
func APIKeyInjectionFactory(name string, _ json.RawMessage, handle framework.Handle) (framework.BBRPlugin, error) {
	plugin, err := NewAPIKeyInjectionPlugin(handle.ReconcilerBuilder, handle.ClientReader())
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin '%s' - %w", APIKeyInjectionPluginType, err)
	}

	return plugin.WithName(name), nil
}

// NewAPIKeyInjectionPlugin creates a new apiKeyInjectionPlugin and returns its pointer
func NewAPIKeyInjectionPlugin(reconcilerBuilder func() *builder.Builder, clientReader client.Reader) (*ApiKeyInjectionPlugin, error) {
	store := newSecretStore()
	reconciler := &secretReconciler{
		Reader: clientReader,
		store:  store,
	}

	if err := reconcilerBuilder().For(&corev1.Secret{}).WithEventFilter(managedLabelPredicate()).Complete(reconciler); err != nil {
		return nil, fmt.Errorf("failed to register Secret reconciler for plugin '%s' - %w", APIKeyInjectionPluginType, err)
	}

	return (&ApiKeyInjectionPlugin{
		typedName: plugin.TypedName{
			Type: APIKeyInjectionPluginType,
			Name: APIKeyInjectionPluginType,
		},
		authHeadersGenerators: map[string]auth.AuthHeadersGenerator{
			provider.OpenAI:        &auth.SimpleAuthGenerator{HeaderName: "Authorization", HeaderValuePrefix: "Bearer "},
			provider.Anthropic:     &auth.SimpleAuthGenerator{HeaderName: "x-api-key"},
			provider.AzureOpenAI:   &auth.SimpleAuthGenerator{HeaderName: "api-key"},
			provider.Vertex:        &auth.SimpleAuthGenerator{HeaderName: "Authorization", HeaderValuePrefix: "Bearer "},
			provider.BedrockOpenAI: &auth.SimpleAuthGenerator{HeaderName: "Authorization", HeaderValuePrefix: "Bearer "},
		},
		store: store,
	}), nil
}

// ApiKeyInjectionPlugin injects an API key from a Kubernetes Secret into the request headers.
// The Secret is identified by its namespaced name from CycleState. The provider (e.g., openai, anthropic)
// determines which header name and value format are used.
type ApiKeyInjectionPlugin struct {
	typedName             plugin.TypedName
	authHeadersGenerators map[string]auth.AuthHeadersGenerator
	store                 *secretStore
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *ApiKeyInjectionPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the name of this plugin instance.
func (p *ApiKeyInjectionPlugin) WithName(name string) *ApiKeyInjectionPlugin {
	p.typedName.Name = name
	return p
}

// ProcessRequest reads the credential Secret reference and provider from CycleState (written by model-provider-resolver),
// looks up the API key in the store, and injects provider-specific auth headers into the request.
func (p *ApiKeyInjectionPlugin) ProcessRequest(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest) error {
	if request == nil || request.Headers == nil {
		return errcommon.Error{Code: errcommon.BadRequest, Msg: "request or headers is nil"}
	}

	// Check if this is an external model (provider set by model-provider-resolver).
	// Internal models have no provider in CycleState and don't need API key injection.
	providerName, err := framework.ReadCycleStateKey[string](cycleState, state.ProviderKey)
	if err != nil || providerName == "" {
		return nil
	}

	credsName, err := framework.ReadCycleStateKey[string](cycleState, state.CredsRefName)
	if err != nil || credsName == "" {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("provider '%s' is missing credentialRef", providerName)}
	}
	credsNamespace, err := framework.ReadCycleStateKey[string](cycleState, state.CredsRefNamespace)
	if err != nil || credsNamespace == "" {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("provider '%s' is missing credentialRef namespace", providerName)}
	}

	secretKey := fmt.Sprintf("%s/%s", credsNamespace, credsName)
	credentials, found := p.store.get(secretKey)
	if !found {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("provider '%s' credentials not found", providerName)}
	}

	generator, ok := p.authHeadersGenerators[providerName]
	if !ok {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("unsupported provider - '%s'", providerName)}
	}

	authHeaders, err := generator.GenerateAuthHeaders(credentials)
	if err != nil {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("failed to generate auth headers for provider '%s': %v", providerName, err)}
	}

	for headerKey, headerValue := range authHeaders {
		request.SetHeader(headerKey, headerValue)
	}

	log.FromContext(ctx).V(logutil.VERBOSE).Info("auth headers injected", "provider", providerName)
	return nil
}
