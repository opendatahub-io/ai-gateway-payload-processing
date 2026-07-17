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
	"maps"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"

	authgenerator "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/apikey-injection/auth-generator"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	// APIKeyInjectionPluginType is the registered name for this plugin in the BBR registry.
	APIKeyInjectionPluginType = "apikey-injection"
)

// compile-time interface check
var _ requesthandling.RequestProcessor = &ApiKeyInjectionPlugin{}

// APIKeyInjectionFactory defines the factory function for ApiKeyInjectionPlugin.
func APIKeyInjectionFactory(name string, _ json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	plugin, err := NewAPIKeyInjectionPlugin(handle.Context(), handle.ReconcilerBuilder, handle.Client())
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin '%s' - %w", APIKeyInjectionPluginType, err)
	}

	return plugin.WithName(name), nil
}

// NewAPIKeyInjectionPlugin creates a new apiKeyInjectionPlugin and returns its pointer.
// It sets up a label-filtered informer cache so only Secrets matching the
// ipp-managed label are listed from the API server.
func NewAPIKeyInjectionPlugin(ctx context.Context, reconcilerBuilder func() *builder.Builder, clientReader client.Reader) (*ApiKeyInjectionPlugin, error) {
	cacheCtx, cancel := context.WithCancel(ctx)

	store := newSecretStore()
	reconciler := &secretReconciler{
		Reader: clientReader,
		store:  store,
	}

	filteredCache, err := newFilteredSecretCache(cacheCtx)
	if err != nil {
		cancel()
		return nil, err
	}

	var secretObj client.Object = &corev1.Secret{}
	if err := reconcilerBuilder().
		Named("apikey-injection-secret-watcher").
		WatchesRawSource(
			source.Kind(filteredCache, secretObj, &handler.EnqueueRequestForObject{}),
		).
		Complete(reconciler); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to register Secret reconciler for plugin '%s' - %w", APIKeyInjectionPluginType, err)
	}

	return (&ApiKeyInjectionPlugin{
		typedName: plugin.TypedName{
			Type: APIKeyInjectionPluginType,
			Name: APIKeyInjectionPluginType,
		},
		authHeadersGenerators: map[auth.Auth]authgenerator.AuthHeadersGenerator{
			auth.APIKey: authgenerator.NewAPIKeyAuthGenerator(),
			auth.SigV4:  authgenerator.NewSigV4AuthGenerator(),
			auth.OAuth2: authgenerator.NewGCPOAuth2Generator(),
		},
		store:     store,
		cancelCtx: cancel,
	}), nil
}

// ApiKeyInjectionPlugin injects an API key from a Kubernetes Secret into the request headers.
// The Secret is identified by its namespaced name from CycleState. The Auth.Type (from the CR)
// determines which header(s) and value(s) format are used.
type ApiKeyInjectionPlugin struct {
	typedName             plugin.TypedName
	authHeadersGenerators map[auth.Auth]authgenerator.AuthHeadersGenerator
	store                 *secretStore
	cancelCtx             context.CancelFunc
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

// ProcessRequest reads the credential Secret reference and authType from CycleState (written by model-provider-resolver),
// looks up the API key in the store, and injects auth headers into the request.
func (p *ApiKeyInjectionPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	// Check if this is an external model (authType set by model-provider-resolver).
	// Internal models have no authType in CycleState and don't need API key injection.
	authType, err := plugin.ReadCycleStateKey[auth.Auth](cycleState, state.AuthTypeKey)
	if err != nil || authType == "" {
		return nil
	}

	// "none" auth: strip existing auth headers without injecting new ones.
	// Used for hub-to-spoke routing where mTLS handles authentication.
	if authType == auth.None {
		request.RemoveHeader("authorization")
		logger.Info("auth headers stripped", "authType", authType)
		return nil
	}

	credsName, err := plugin.ReadCycleStateKey[string](cycleState, state.CredsRefName)
	if err != nil || credsName == "" {
		logger.Error(err, "credentialRef name missing", "authType", authType)
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("authType '%s' is missing credentialRef", authType)}
	}
	credsNamespace, err := plugin.ReadCycleStateKey[string](cycleState, state.CredsRefNamespace)
	if err != nil || credsNamespace == "" {
		logger.Error(err, "credentialRef namespace missing", "authType", authType)
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("authType '%s' is missing credentialRef namespace", authType)}
	}

	credentialsData, found := p.store.get(credsNamespace, credsName)
	if !found {
		logger.Error(nil, "credentials not found in store", "authType", authType)
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("authType '%s' credentials not found", authType)}
	}

	generator, ok := p.authHeadersGenerators[authType]
	if !ok {
		logger.Error(nil, "unsupported auth type for auth generation", "authType", authType)
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("unsupported authType - '%s'", authType)}
	}

	extraData, err := generator.ExtractRequestData(cycleState, request)
	if err != nil {
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("failed to extract request data for authType '%s': %v", authType, err)}
	}
	if len(extraData) > 0 {
		merged := make(map[string]string, len(credentialsData)+len(extraData))
		maps.Copy(merged, credentialsData)
		maps.Copy(merged, extraData)
		credentialsData = merged
	}

	authHeaders, err := generator.GenerateAuthHeaders(credentialsData)
	if err != nil {
		logger.Error(err, "auth header generation failed", "authType", authType)
		return errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("failed to generate auth headers for authType '%s': %v", authType, err)}
	}

	for headerKey, headerValue := range authHeaders {
		request.SetHeader(headerKey, headerValue)
	}

	logger.Info("auth headers injected", "authType", authType)
	return nil
}

// newFilteredSecretCache creates a controller-runtime cache that restricts the
// Secret informer to only list/watch Secrets labeled with the managed label.
// This is a defense-in-depth measure: even though the RBAC ClusterRole grants
// broad Secret access (required for cross-namespace watches), the informer
// never fetches or caches unrelated Secrets.
func newFilteredSecretCache(ctx context.Context) (cache.Cache, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get rest config for filtered Secret cache: %w", err)
	}

	filteredCache, err := cache.New(cfg, cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Label: labels.SelectorFromSet(labels.Set{
					managedLabel: "true",
				}),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create filtered Secret cache: %w", err)
	}

	go func() {
		if err := filteredCache.Start(ctx); err != nil {
			ctrl.Log.WithName(APIKeyInjectionPluginType).Error(err, "filtered Secret cache stopped unexpectedly")
		}
	}()

	syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !filteredCache.WaitForCacheSync(syncCtx) {
		return nil, fmt.Errorf("filtered Secret cache failed to sync within deadline")
	}

	return filteredCache, nil
}
