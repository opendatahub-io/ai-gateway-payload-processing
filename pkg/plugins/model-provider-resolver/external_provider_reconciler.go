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

package model_provider_resolver

import (
	"context"
	"fmt"
	"maps"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
)

// defaultAuthHeaders maps providers that use a non-standard auth header name.
// Providers not listed here fall back to "Authorization" with "Bearer " prefix
// in the auth generator.
var defaultAuthHeaders = map[string]string{
	provider.Anthropic: "x-api-key",
	provider.Azure:     "api-key",
	// TODO to be removed
	provider.AzureOpenAI: "api-key",
}

type externalProviderReconciler struct {
	client.Reader
	store *infoStore
}

func (r *externalProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	logger.Info("reconciling ExternalProvider", "name", req.Name, "namespace", req.Namespace)

	provider := &inferencev1alpha1.ExternalProvider{}
	err := r.Get(ctx, req.NamespacedName, provider)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get ExternalProvider: %w", err)
	}

	if errors.IsNotFound(err) || !provider.GetDeletionTimestamp().IsZero() {
		r.store.deleteProvider(req.NamespacedName)
		logger.Info("ExternalProvider removed from store", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	config := buildConfigWithDefaults(provider.Spec.Provider, provider.Spec.Config)
	authType := mapCRDAuthType(provider.Spec.Auth.Type)
	r.store.addOrUpdateProvider(req.NamespacedName, &providerInfo{
		provider:        provider.Spec.Provider,
		endpoint:        provider.Spec.Endpoint,
		auth:            authType,
		secretName:      provider.Spec.Auth.SecretRef.Name,
		secretNamespace: req.Namespace,
		config:          config,
	})

	logger.Info("updated provider store", "provider", provider.Spec.Provider, "endpoint", provider.Spec.Endpoint)
	return ctrl.Result{}, nil
}

// mapCRDAuthType maps CRD auth.type values to internal auth constants.
// The ExternalProvider CRD uses "simple" for API-key auth, while the
// internal auth package uses "apikey".
func mapCRDAuthType(crdType string) auth.Auth {
	if crdType == "simple" {
		return auth.APIKey
	}
	return auth.Auth(crdType)
}

// buildConfigWithDefaults returns a config map that starts from provider-specific
// defaults and then applies any user-supplied values from the CR on top.
func buildConfigWithDefaults(providerName string, userConfig map[string]string) map[string]string {
	config := map[string]string{}
	if headerName, ok := defaultAuthHeaders[providerName]; ok {
		config[auth.APIKeyAuthHeaderName] = headerName
	}
	maps.Copy(config, userConfig)
	return config
}
