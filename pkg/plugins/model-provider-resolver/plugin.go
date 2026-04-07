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
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
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
	modelInfoStore := newModelInfoStore()
	externalModelReconciler := &externalModelReconciler{
		Reader: clientReader,
		store:  modelInfoStore,
	}

	// Watch ExternalModel CRDs directly (no MaaS dependency)
	externalModelObj := &unstructured.Unstructured{}
	externalModelObj.SetGroupVersionKind(externalModelGVK)

	if err := reconcilerBuilder().For(externalModelObj).Complete(externalModelReconciler); err != nil {
		return nil, fmt.Errorf("failed to register ExternalModel reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	maasModelRefReconciler := &maasModelRefReconciler{
		Reader: clientReader,
		store:  modelInfoStore,
	}

	// Watch MaaSModelRef CRDs directly (no MaaS dependency)
	maasModelRefObj := &unstructured.Unstructured{}
	maasModelRefObj.SetGroupVersionKind(maasModelRefGVK)

	if err := reconcilerBuilder().For(maasModelRefObj).Complete(maasModelRefReconciler); err != nil {
		return nil, fmt.Errorf("failed to register MaaSModelRef reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}

	return &ModelProviderResolverPlugin{
		typedName:      plugin.TypedName{Type: ModelProviderResolverPluginType, Name: ModelProviderResolverPluginType},
		modelInfoStore: modelInfoStore,
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
// from the modelInfoStore (populated by ExternalModel reconciler), and writes model, provider
// and credential reference info to CycleState.
func (p *ModelProviderResolverPlugin) ProcessRequest(ctx context.Context, cycleState *framework.CycleState, request *framework.InferenceRequest) error {
	model, ok := request.Body["model"].(string)
	if !ok || model == "" {
		return nil // not an inference request (e.g. API key management, model listing)
	}

	maasModelRefKey := parseMaaSModelRefKeyFromPath(request.Headers[":path"])
	if maasModelRefKey == nil {
		return nil // wasn't able to parse namespaced name
	}

	externalModelInfo, found := p.modelInfoStore.getModelInfo(*maasModelRefKey)
	if !found { // info is stored only for external models
		return nil // this is not considered an error, we just need to skip if it's internal model
	}

	// if there's a mismatch it's an error, we don't want to proceed
	if externalModelInfo.targetModel != model {
		return errcommon.Error{Code: errcommon.BadRequest, Msg: fmt.Sprintf("model in request body '%s' doesn't match MaaSModelRef", model)}
	}

	// info of external model written to cycle state for next plugins
	cycleState.Write(state.ProviderKey, externalModelInfo.provider)
	cycleState.Write(state.ModelKey, externalModelInfo.targetModel)
	cycleState.Write(state.CredsRefName, externalModelInfo.secretName)
	cycleState.Write(state.CredsRefNamespace, externalModelInfo.secretNamespace)

	return nil
}

// parseMaaSModelRefKeyFromPath extracts the MaaSModelRef namespace and name from :path.
// Layout: /<namespace>/<maasModelRefName>/... (e.g. /llm/my-ref/v1/chat/completions).
// A query string on :path is stripped first. Leading/trailing slashes are trimmed so
// strings.Split after that yields namespace and name as the first two segments.
func parseMaaSModelRefKeyFromPath(relativeUrlPath string) *types.NamespacedName {
	relativeUrlPath = strings.TrimSpace(relativeUrlPath)
	if relativeUrlPath == "" {
		return nil
	}
	if index := strings.IndexByte(relativeUrlPath, '?'); index >= 0 {
		relativeUrlPath = relativeUrlPath[:index] // remove query params
	}
	relativeUrlPath = strings.Trim(relativeUrlPath, "/")
	if relativeUrlPath == "" {
		return nil
	}
	segments := strings.Split(relativeUrlPath, "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return nil
	}
	return &types.NamespacedName{Namespace: segments[0], Name: segments[1]}
}
