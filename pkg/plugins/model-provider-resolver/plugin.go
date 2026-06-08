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
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	ModelProviderResolverPluginType = "model-provider-resolver"
)

var _ framework.RequestProcessor = &ModelProviderResolverPlugin{}

// ModelProviderResolverFactory defines the factory function for ModelProviderResolverPlugin.
func ModelProviderResolverFactory(name string, _ json.RawMessage, handle framework.Handle) (framework.BBRPlugin, error) {
	plugin, err := NewModelProviderResolver(handle.ReconcilerBuilder, handle.Client())
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	return plugin.WithName(name), nil
}

// NewModelProviderResolver registers store reconcilers for inference.opendatahub.io
// ExternalProvider and ExternalModel CRDs.
func NewModelProviderResolver(reconcilerBuilder func() *builder.Builder, k8sClient client.Client) (*ModelProviderResolverPlugin, error) {
	utilruntime.Must(inferencev1alpha1.AddToScheme(k8sClient.Scheme()))
	store := newInfoStore()

	// Watch ExternalProvider CRDs (inference.opendatahub.io) using typed client
	providerReconciler := &externalProviderReconciler{Reader: k8sClient, store: store}
	if err := reconcilerBuilder().For(&inferencev1alpha1.ExternalProvider{}).Complete(providerReconciler); err != nil {
		return nil, fmt.Errorf("failed to register ExternalProvider reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	// Watch ExternalModel CRDs (inference.opendatahub.io) using typed client.
	// Cross-watch ExternalProviders so credential/endpoint changes propagate.
	modelReconciler := &externalModelReconciler{Reader: k8sClient, store: store}
	mapProviderToModels := func(ctx context.Context, obj client.Object) []reconcile.Request {
		provider := obj.(*inferencev1alpha1.ExternalProvider)
		modelList := &inferencev1alpha1.ExternalModelList{}
		if err := k8sClient.List(ctx, modelList, client.InNamespace(provider.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list ExternalModels for provider mapping",
				"provider", provider.Name, "namespace", provider.Namespace)
			return nil
		}
		var requests []reconcile.Request
		for i := range modelList.Items {
			for _, ref := range modelList.Items[i].Spec.ExternalProviderRefs {
				if ref.Ref.Name == provider.Name {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: modelList.Items[i].Name, Namespace: modelList.Items[i].Namespace},
					})
				}
			}
		}
		return requests
	}
	if err := reconcilerBuilder().
		For(&inferencev1alpha1.ExternalModel{}).
		Named("inference-externalmodel").
		Watches(&inferencev1alpha1.ExternalProvider{}, handler.EnqueueRequestsFromMapFunc(mapProviderToModels)).
		Complete(modelReconciler); err != nil {
		return nil, fmt.Errorf("failed to register ExternalModel reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	return &ModelProviderResolverPlugin{
		typedName: plugin.TypedName{Type: ModelProviderResolverPluginType, Name: ModelProviderResolverPluginType},
		store:     store,
	}, nil
}

// ModelProviderResolverPlugin resolves model names to provider info by watching ExternalModel CRDs.
// It writes the model, provider and credential reference to CycleState for downstream plugins
// (api-translation, api-key-injection).
type ModelProviderResolverPlugin struct {
	typedName plugin.TypedName
	store     *infoStore
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *ModelProviderResolverPlugin) TypedName() plugin.TypedName { return p.typedName }

// WithName sets the name of the plugin instance.
func (p *ModelProviderResolverPlugin) WithName(name string) *ModelProviderResolverPlugin {
	p.typedName.Name = name
	return p
}

// ProcessRequest reads the model name from the request body, resolves the provider
// from the store (populated by ExternalModel reconciler), and writes model, provider
// and credential reference info to CycleState.
func (p *ModelProviderResolverPlugin) ProcessRequest(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	model, ok := request.Body["model"].(string)
	if !ok || model == "" {
		return nil // not an inference request (e.g. API key management, model listing)
	}

	log.FromContext(ctx).V(logutil.VERBOSE).Info("received incoming request", "path", request.Headers[":path"])
	relativePath := sanitizePath(request.Headers[":path"])

	segments := strings.Split(relativePath, "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("wasn't able to parse namespaced name from path", "path", relativePath)
		return nil
	}

	modelKey := types.NamespacedName{Namespace: segments[0], Name: segments[1]}
	log.FromContext(ctx).V(logutil.VERBOSE).Info("exported namespaced name from path", "key", modelKey)

	externalModelInfo, found := p.store.getModel(modelKey)
	if !found {
		return nil // not an external model — pass through for internal models
	}

	if !strings.HasSuffix(relativePath, "chat/completions") {
		logger.Error(nil, "unsupported route for external model", "model", modelKey.String(), "path", relativePath)
		return errcommon.Error{Code: errcommon.BadRequest, Msg: "only /chat/completions input type is supported"}
	}

	// model in request body must match the ExternalModel's targetModel
	if externalModelInfo.targetModel != model {
		logger.Error(nil, "model mismatch between request body and ExternalModel", "requestModel", model, "externalModel", externalModelInfo.targetModel)
		return errcommon.Error{Code: errcommon.NotFound, Msg: fmt.Sprintf("model in request body '%s' doesn't match ExternalModel", model)}
	}

	// write resolved info to CycleState for downstream plugins (api-translation, apikey-injection)
	cycleState.Write(state.ProviderKey, externalModelInfo.provider)
	cycleState.Write(state.ModelKey, externalModelInfo.targetModel)
	cycleState.Write(state.CredsRefName, externalModelInfo.secretName)
	cycleState.Write(state.CredsRefNamespace, externalModelInfo.secretNamespace)
	if len(externalModelInfo.config) > 0 {
		cycleState.Write(state.ModelConfigKey, externalModelInfo.config)
	}

	logger.Info("external model resolved", "model", modelKey.String(), "provider", externalModelInfo.provider)
	return nil
}

func sanitizePath(relativeUrlPath string) string {
	relativeUrlPath = strings.TrimSpace(relativeUrlPath)
	if index := strings.IndexByte(relativeUrlPath, '?'); index >= 0 {
		relativeUrlPath = relativeUrlPath[:index]
	}
	return strings.Trim(relativeUrlPath, "/")
}
