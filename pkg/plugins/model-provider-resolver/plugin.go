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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	ModelProviderResolverPluginType = "model-provider-resolver"
)

// compile-time type validation
var _ framework.RequestProcessor = &ModelProviderResolverPlugin{}

// ModelProviderResolverFactory defines the factory function for ModelProviderResolverPlugin
func ModelProviderResolverFactory(name string, _ json.RawMessage, handle framework.Handle) (framework.BBRPlugin, error) {
	plugin, err := NewModelProviderResolver(handle.ReconcilerBuilder, handle.ClientReader())
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	return plugin.WithName(name), nil
}

func NewModelProviderResolver(reconcilerBuilder func() *builder.Builder, clientReader client.Reader) (*ModelProviderResolverPlugin, error) {
	providerStore := newProviderInfoStore()
	modelStore := newModelInfoStore()

	// Watch ExternalProvider CRDs (inference.opendatahub.io/v1alpha1)
	providerObj := &unstructured.Unstructured{}
	providerObj.SetGroupVersionKind(externalProviderGVK)
	providerReconciler := &externalProviderReconciler{
		Reader: clientReader,
		store:  providerStore,
	}
	if err := reconcilerBuilder().For(providerObj).Complete(providerReconciler); err != nil {
		return nil, fmt.Errorf("failed to register ExternalProvider reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	// Watch ExternalModel CRDs (inference.opendatahub.io/v1alpha1)
	// Cross-watch ExternalProviders so credential/endpoint changes propagate to modelStore
	modelObj := &unstructured.Unstructured{}
	modelObj.SetGroupVersionKind(externalModelGVK)
	modelReconciler := &externalModelReconciler{
		Reader:        clientReader,
		modelStore:    modelStore,
		providerStore: providerStore,
	}
	mapProviderToModels := func(ctx context.Context, obj client.Object) []reconcile.Request {
		providerName := obj.GetName()
		providerNamespace := obj.GetNamespace()
		modelList := &unstructured.UnstructuredList{}
		modelList.SetGroupVersionKind(externalModelGVK)
		if err := clientReader.List(ctx, modelList, client.InNamespace(providerNamespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list ExternalModels for provider mapping",
				"provider", providerName, "namespace", providerNamespace)
			return nil
		}
		var requests []reconcile.Request
		for i := range modelList.Items {
			refs, _, _ := unstructured.NestedSlice(modelList.Items[i].Object, "spec", "externalProviderRefs")
			if len(refs) == 0 {
				continue
			}
			refMap, ok := refs[0].(map[string]any)
			if !ok {
				continue
			}
			if nestedString(refMap, "ref", "name") == providerName {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      modelList.Items[i].GetName(),
						Namespace: modelList.Items[i].GetNamespace(),
					},
				})
			}
		}
		return requests
	}
	providerWatchObj := &unstructured.Unstructured{}
	providerWatchObj.SetGroupVersionKind(externalProviderGVK)
	if err := reconcilerBuilder().
		For(modelObj).
		Watches(providerWatchObj, handler.EnqueueRequestsFromMapFunc(mapProviderToModels)).
		Complete(modelReconciler); err != nil {
		return nil, fmt.Errorf("failed to register ExternalModel reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	return &ModelProviderResolverPlugin{
		typedName:      plugin.TypedName{Type: ModelProviderResolverPluginType, Name: ModelProviderResolverPluginType},
		modelInfoStore: modelStore,
	}, nil
}

// ModelProviderResolverPlugin resolves model names to provider info by watching ExternalModel CRDs.
// It writes the model, provider and credential reference to CycleState for downstream plugins
// (api-translation, api-key-injection).
type ModelProviderResolverPlugin struct {
	typedName      plugin.TypedName
	modelInfoStore *modelInfoStore
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *ModelProviderResolverPlugin) TypedName() plugin.TypedName {
	return p.typedName
}

// WithName sets the name of the plugin instance.
func (p *ModelProviderResolverPlugin) WithName(name string) *ModelProviderResolverPlugin {
	p.typedName.Name = name
	return p
}

// ProcessRequest reads the model name from the request body, resolves the provider
// from the modelInfoStore (populated by ExternalModel reconciler), selects a provider
// using weighted random if multiple are configured, and writes provider info to CycleState.
func (p *ModelProviderResolverPlugin) ProcessRequest(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest) error {
	model, ok := request.Body["model"].(string)
	if !ok || model == "" {
		return nil
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

	modelInfo, found := p.modelInfoStore.getModelInfo(modelKey)
	if !found {
		return nil
	}

	if !strings.HasSuffix(relativePath, "chat/completions") {
		return errcommon.Error{Code: errcommon.BadRequest, Msg: "only /chat/completions input type is supported"}
	}

	// Validate request model matches the ExternalModel CR name (client-facing name)
	if modelInfo.modelName != model {
		return errcommon.Error{Code: errcommon.NotFound, Msg: fmt.Sprintf("model in request body '%s' doesn't match ExternalModel '%s'", model, modelInfo.modelName)}
	}

	// Select provider using weighted random
	selected := modelInfo.selectProvider()

	// Rewrite the model field to the provider's targetModel
	request.Body["model"] = selected.targetModel

	// Write selected provider info to CycleState for downstream plugins
	cycleState.Write(state.ProviderKey, selected.provider)
	cycleState.Write(state.ModelKey, selected.targetModel)
	cycleState.Write(state.CredsRefName, selected.secretName)
	cycleState.Write(state.CredsRefNamespace, selected.secretNamespace)
	cycleState.Write(state.APIFormatKey, selected.apiFormat)
	cycleState.Write(state.SelectedProviderKey, selected.providerName)
	if len(selected.config) > 0 {
		cycleState.Write(state.ProviderConfigKey, selected.config)
	}

	// Set header for HTTPRoute matching — tells Envoy which provider backend to use
	request.SetHeader("X-Selected-Provider", selected.providerName)

	return nil
}

func sanitizePath(relativeUrlPath string) string {
	relativeUrlPath = strings.TrimSpace(relativeUrlPath)

	if index := strings.IndexByte(relativeUrlPath, '?'); index >= 0 {
		relativeUrlPath = relativeUrlPath[:index] // remove query params
	}

	return strings.Trim(relativeUrlPath, "/")
}
