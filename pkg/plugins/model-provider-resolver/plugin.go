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
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	modelInfoStore := newModelInfoStore()
	reconciler := &externalModelReconciler{
		Reader: clientReader,
		store:  modelInfoStore,
	}

	// Watch ExternalModel CRDs directly (no MaaS dependency)
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalModelGVK)

	if err := reconcilerBuilder().For(obj).Complete(reconciler); err != nil {
		return nil, fmt.Errorf("failed to register ExternalModel reconciler for plugin '%s' - %w", ModelProviderResolverPluginType, err)
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

	log.FromContext(ctx).V(logutil.VERBOSE).Info("received incoming request", "path", request.Headers[":path"])
	relativePath := sanitizePath(request.Headers[":path"])

	segments := strings.Split(relativePath, "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("wasn't able to parse namespaced name from path", "path", relativePath)
		return nil
	}

	modelKey := types.NamespacedName{Namespace: segments[0], Name: segments[1]}
	log.FromContext(ctx).V(logutil.VERBOSE).Info("exported namespaced name from path", "key", modelKey)

	// Authorize namespace boundary before any store lookup so callers cannot probe
	// cross-namespace ExternalModel existence (same 403 regardless of whether the model exists).
	requestNamespace, ok := extractRequestNamespace(request.Headers)
	if !ok {
		return errcommon.Error{
			Code: errcommon.Forbidden,
			Msg:  "unable to validate namespace boundary for external model request",
		}
	}
	if requestNamespace != modelKey.Namespace {
		return errcommon.Error{
			Code: errcommon.Forbidden,
			Msg:  fmt.Sprintf("cross-namespace access denied: request namespace '%s' cannot access model namespace '%s'", requestNamespace, modelKey.Namespace),
		}
	}

	externalModelInfo, found := p.modelInfoStore.getModelInfo(modelKey)
	if !found { // info is stored only for external models
		return nil // this is not considered an error, we just need to skip if it's internal model
	}

	if !strings.HasSuffix(relativePath, "chat/completions") { // no support for other input types
		return errcommon.Error{Code: errcommon.BadRequest, Msg: "only /chat/completions input type is supported"}

	}

	// if there's a mismatch it's an error, we don't want to proceed
	if externalModelInfo.targetModel != model {
		return errcommon.Error{Code: errcommon.NotFound, Msg: fmt.Sprintf("model in request body '%s' doesn't match ExternalModel", model)}
	}

	// info of external model written to cycle state for next plugins
	cycleState.Write(state.ProviderKey, externalModelInfo.provider)
	cycleState.Write(state.ModelKey, externalModelInfo.targetModel)
	cycleState.Write(state.CredsRefName, externalModelInfo.secretName)
	cycleState.Write(state.CredsRefNamespace, externalModelInfo.secretNamespace)

	return nil
}

func sanitizePath(relativeUrlPath string) string {
	relativeUrlPath = strings.TrimSpace(relativeUrlPath)

	if index := strings.IndexByte(relativeUrlPath, '?'); index >= 0 {
		relativeUrlPath = relativeUrlPath[:index] // remove query params
	}

	return strings.Trim(relativeUrlPath, "/")
}

var namespaceHintHeaders = []string{
	"x-ai-gateway-request-namespace",
	"x-kubernetes-namespace",
	"x-namespace",
}

const spiffeNamespacePrefix = "/ns/"

// namespaceFromXFCCSPIFFE returns the workload namespace from the first SPIFFE URI in
// x-forwarded-client-cert that contains the standard .../ns/<namespace>/... path segment.
func namespaceFromXFCCSPIFFE(xfccValue string) (string, bool) {
	for _, part := range strings.Split(xfccValue, ";") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "URI=") {
			continue
		}
		uriString := strings.Trim(strings.TrimPrefix(part, "URI="), "\"")
		parsedURI, err := url.Parse(uriString)
		if err != nil {
			continue
		}
		path := parsedURI.EscapedPath()
		prefixIndex := strings.Index(path, spiffeNamespacePrefix)
		if prefixIndex < 0 {
			continue
		}

		namespacePath := path[prefixIndex+len(spiffeNamespacePrefix):]
		namespaceSegments := strings.SplitN(namespacePath, "/", 2)
		namespace := strings.TrimSpace(namespaceSegments[0])
		if namespace != "" {
			return namespace, true
		}
	}
	return "", false
}

// extractRequestNamespace resolves the authenticated requester namespace.
// When x-forwarded-client-cert contains a SPIFFE identity with a namespace segment, that
// value is authoritative; if any hint header disagrees, the request is rejected.
// If no SPIFFE namespace is found, hint headers are used (e.g. injected by gateway/MaaS).
func extractRequestNamespace(headers map[string]string) (string, bool) {
	xfccValue := strings.TrimSpace(headers["x-forwarded-client-cert"])
	if xfccValue != "" {
		if ns, ok := namespaceFromXFCCSPIFFE(xfccValue); ok {
			for _, headerName := range namespaceHintHeaders {
				if hinted := strings.TrimSpace(headers[headerName]); hinted != "" && hinted != ns {
					return "", false
				}
			}
			return ns, true
		}
	}

	for _, headerName := range namespaceHintHeaders {
		if namespace := strings.TrimSpace(headers[headerName]); namespace != "" {
			return namespace, true
		}
	}

	return "", false
}
