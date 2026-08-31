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
	"math/rand/v2"
	"net"
	"os"
	"strings"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/dynamicmetadata"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/provider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	ModelProviderResolverPluginType = "model-provider-resolver"

	// SelectedProviderHeader is set by the plugin on each request to drive
	// Envoy routing. The HTTPRoute reconciler creates per-provider route rules
	// that match on this header, ensuring the destination and credential are
	// always consistent.
	SelectedProviderHeader = "x-ipp-selected-provider"
)

var _ requesthandling.RequestProcessor = &ModelProviderResolverPlugin{}

type pluginConfig struct {
	HubMode bool `json:"hubMode"`
}

// ModelProviderResolverFactory defines the factory function for ModelProviderResolverPlugin.
func ModelProviderResolverFactory(name string, configJSON json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	var cfg pluginConfig
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config for plugin '%s': %w", ModelProviderResolverPluginType, err)
		}
	}

	p, err := NewModelProviderResolver(handle.ReconcilerBuilder, handle.Client())
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin '%s' - %w", ModelProviderResolverPluginType, err)
	}
	p.hubMode = cfg.HubMode
	p.clusterName = strings.TrimSpace(os.Getenv(state.ClusterNameEnv))

	return p.WithName(name), nil
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
//
// The plugin performs routing loop detection per issue #343: incoming
// X-Origin-Cluster is compared to this cluster's CLUSTER_NAME. A match is a
// true A→B→A loop-back and is rejected. A different origin (A→B→C) is allowed.
// On an outgoing remote-maas hop the original origin is preserved, or this
// cluster's name is injected if the header is absent. Loop detection is
// disabled when CLUSTER_NAME is unset.
type ModelProviderResolverPlugin struct {
	typedName   plugin.TypedName
	store       *infoStore
	hubMode     bool
	clusterName string
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
//
// The method also performs routing loop detection (#343):
//   - Rejects when X-Origin-Cluster matches CLUSTER_NAME (A→B→A).
//   - Allows a different origin (A→B→C) and local models.
//   - Injects/restores X-Origin-Cluster only on remote-maas hops and hub
//     TRANSFORM (spoke) hops — not on direct third-party APIs.
//   - Hub PROPOSE does not inject (it is not an outbound hop). TRANSFORM does.
func (p *ModelProviderResolverPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	model, ok := request.Body["model"].(string)
	if !ok || model == "" {
		return nil // not an inference request (e.g. API key management, model listing)
	}

	log.FromContext(ctx).V(logutil.VERBOSE).Info("received incoming request", "path", request.Headers[":path"])

	// Resolve by model name: prefer X-Gateway-Model-Name header (set by body-field-to-header),
	// fall back to request body model field. This supports both single-URL and per-model-URL patterns.
	modelName := request.Headers["x-gateway-model-name"]
	if modelName == "" {
		modelName = model
	}

	// Record the client's API format for every inference request on a
	// recognized path — even when no ExternalModel matches — so downstream
	// consumers (e.g. passthrough-profile-picker) can distinguish an
	// internal-model request from a request this plugin never processed.
	relativePath := sanitizePath(request.Headers[":path"])
	inputFormat := detectInputAPIFormat(relativePath)
	if inputFormat != "" {
		cycleState.Write(state.InputAPIFormatKey, inputFormat)
	}

	modelInfo, found := p.store.getModelByName(modelName)
	if !found {
		// LLMISvc BBR: client sent publisher ID (publishers/{ns}/models/{name}) in body,
		// as returned by KServe GET /v1/models. X-Gateway-Model-Name header already has
		// the publisher ID (set by body-field-to-header) — do not modify it, KServe routes on it.
		// Rewrite body model field so vLLM receives just the model name.
		// Write publisher ID to CycleState so ipp-post (metering, api-translation) can use it.
		if strings.HasPrefix(modelName, "publishers/") {
			if parts := strings.SplitN(modelName, "/models/", 2); len(parts) == 2 && parts[1] != "" {
				request.SetBodyField("model", parts[1])
				cycleState.Write(state.ModelKey, modelName)
				logger.Info("LLMISvc BBR: rewrote body model field",
					"original", modelName, "rewritten", parts[1])
			}
		}
		return nil
	}

	logger.Info("resolved model by name", "modelName", modelName)

	if err := p.checkRoutingLoop(ctx, cycleState, request); err != nil {
		return err
	}

	// Hub mode PROPOSE: set dynamic metadata for EPP subset filtering.
	// Incoming loop check already ran; PROPOSE is not an outbound hop so
	// the origin header is not injected here.
	if p.hubMode && request.Headers["x-gateway-destination-endpoint"] == "" {
		return p.propose(ctx, request, modelInfo)
	}

	if inputFormat == "" {
		logger.Error(nil, "unsupported API path for external model", "model", modelName, "path", relativePath)
		return errcommon.Error{Code: errcommon.BadRequest, Msg: "unsupported API endpoint"}
	}

	var ref *resolvedProviderRef
	if p.hubMode {
		ref = findRefByEndpoint(modelInfo.refs, request.Headers["x-gateway-destination-endpoint"])
		if ref == nil {
			return errcommon.Error{Code: errcommon.BadRequest,
				Msg: "no ExternalProvider matches destination " + request.Headers["x-gateway-destination-endpoint"]}
		}
	} else {
		ref = selectByWeight(modelInfo.refs)
		if ref == nil {
			return errcommon.Error{Code: errcommon.BadRequest, Msg: "all providers for model " + modelName + " are disabled (weight 0)"}
		}
	}

	// Drive Envoy routing to the selected provider's backend.
	request.SetHeader(SelectedProviderHeader, ref.providerName)
	request.SetHeader("Host", ref.endpoint)

	if model != ref.targetModel {
		request.SetBodyField("model", ref.targetModel)
	}

	cycleState.Write(state.ProviderKey, ref.provider)
	cycleState.Write(state.ModelKey, ref.targetModel)
	cycleState.Write(state.APIFormatKey, ref.apiFormat)
	cycleState.Write(state.AuthTypeKey, ref.auth)
	cycleState.Write(state.EndpointKey, ref.endpoint)
	cycleState.Write(state.PathKey, ref.path)
	cycleState.Write(state.CredsRefName, ref.secretName)
	cycleState.Write(state.CredsRefNamespace, ref.secretNamespace)
	cycleState.Write(state.ModelConfigKey, ref.config)

	p.restoreOrMarkOrigin(ctx, cycleState, request, ref)

	logger.Info("external model resolved", "model", modelName, "provider", ref.provider, "inputFormat", inputFormat, "apiFormat", ref.apiFormat)
	return nil
}

// checkRoutingLoop rejects a request whose X-Origin-Cluster matches this
// cluster's CLUSTER_NAME (A→B→A). A different origin is allowed (A→B→C).
// Disabled when CLUSTER_NAME is unset. Forbidden (403) is used because
// errcommon has no LoopDetected/508 code.
func (p *ModelProviderResolverPlugin) checkRoutingLoop(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	if p.clusterName == "" {
		return nil
	}
	origin := originCluster(cycleState, request)
	if origin == "" || !strings.EqualFold(origin, p.clusterName) {
		return nil
	}

	log.FromContext(ctx).V(logutil.DEFAULT).Error(nil,
		"routing loop detected: request originated from this cluster",
		"origin", origin, "cluster", p.clusterName)
	return errcommon.Error{
		Code: errcommon.Forbidden,
		Msg:  "routing loop detected: request originated from this cluster",
	}
}

func originCluster(cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) string {
	if cycleState != nil {
		if captured, err := plugin.ReadCycleStateKey[string](cycleState, state.OriginClusterKey); err == nil {
			if v := strings.TrimSpace(captured); v != "" {
				return v
			}
		}
	}
	for key, value := range request.Headers {
		if strings.EqualFold(key, state.OriginClusterHeader) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// restoreOrMarkOrigin puts X-Origin-Cluster back on remote-maas / hub
// TRANSFORM hops after the guard stripped it. The first origin is preserved
// (so A→B→A still sees A); if absent, this cluster's name is injected.
// Direct third-party APIs (openai, anthropic, ...) are skipped.
func (p *ModelProviderResolverPlugin) restoreOrMarkOrigin(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest, ref *resolvedProviderRef) {
	if !shouldInjectOrigin(ref, p.hubMode) {
		return
	}
	origin := originCluster(cycleState, request)
	if origin == "" {
		origin = p.clusterName
	}
	if origin == "" {
		return
	}
	request.SetHeader(state.OriginClusterHeader, origin)
	log.FromContext(ctx).V(logutil.VERBOSE).Info("set origin cluster header",
		"header", state.OriginClusterHeader, "origin", origin)
}

// shouldInjectOrigin is true for explicit remote-maas providers (#343) and
// for hub TRANSFORM (hub→spoke is a cross-cluster hop even when the spoke
// is typed openai). A non-empty path is not a signal: OpenAI/Anthropic CRs
// always set path, and must not receive X-Origin-Cluster.
func shouldInjectOrigin(ref *resolvedProviderRef, hubMode bool) bool {
	if ref == nil {
		return false
	}
	if hubMode {
		return true
	}
	return strings.EqualFold(ref.provider, provider.RemoteMaaS)
}

// detectInputAPIFormat determines the client's API format from the request path suffix.
func detectInputAPIFormat(path string) apiformat.APIFormat {
	switch {
	case strings.HasSuffix(path, "/v1/chat/completions"), path == "v1/chat/completions":
		return apiformat.OpenAIChatCompletions
	case strings.HasSuffix(path, "/v1/embeddings"), path == "v1/embeddings":
		return apiformat.OpenAIEmbeddings
	case strings.HasSuffix(path, "/v1/messages"), path == "v1/messages":
		return apiformat.Messages
	case strings.HasSuffix(path, "/v1/responses"), path == "v1/responses":
		return apiformat.OpenAIResponses
	default:
		return ""
	}
}

// selectByWeight picks a provider ref using weighted random selection.
// Refs with weight <= 0 are skipped (disabled). Returns nil when all
// refs have zero weight (all disabled).
func selectByWeight(refs []*resolvedProviderRef) *resolvedProviderRef {
	totalWeight := 0
	for _, ref := range refs {
		if ref.weight > 0 {
			totalWeight += ref.weight
		}
	}
	if totalWeight == 0 {
		return nil
	}
	r := rand.IntN(totalWeight)
	for _, ref := range refs {
		if ref.weight <= 0 {
			continue
		}
		r -= ref.weight
		if r < 0 {
			return ref
		}
	}
	return refs[len(refs)-1]
}

// propose handles the hub mode PROPOSE phase: collect eligible spoke endpoints
// and set dynamic metadata for the EPP to filter on. No CycleState is written.
func (p *ModelProviderResolverPlugin) propose(ctx context.Context, request *requesthandling.InferenceRequest, modelInfo *externalModelInfo) error {
	var endpoints []string
	for _, ref := range modelInfo.refs {
		if ref.weight > 0 {
			endpoints = append(endpoints, ref.endpoint)
		}
	}
	if len(endpoints) == 0 {
		return nil
	}

	log.FromContext(ctx).V(logutil.DEFAULT).Info("hub mode PROPOSE: setting endpoint subset",
		"model", modelInfo.modelName, "endpoints", endpoints)
	return dynamicmetadata.SetEndpointSubset(request, endpoints)
}

// findRefByEndpoint returns the first eligible ref whose endpoint hostname
// matches the destination. Ports are stripped from both sides before comparison.
// Refs with weight <= 0 are skipped: PROPOSE only advertises eligible spokes, so
// a stale or spoofed x-gateway-destination-endpoint pointing at a disabled spoke
// must not route there — it is treated as no match.
func findRefByEndpoint(refs []*resolvedProviderRef, destination string) *resolvedProviderRef {
	host := stripPort(destination)
	for _, ref := range refs {
		if ref.weight <= 0 {
			continue
		}
		if stripPort(ref.endpoint) == host {
			return ref
		}
	}
	return nil
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func sanitizePath(relativeUrlPath string) string {
	relativeUrlPath = strings.TrimSpace(relativeUrlPath)
	if index := strings.IndexByte(relativeUrlPath, '?'); index >= 0 {
		relativeUrlPath = relativeUrlPath[:index]
	}
	return strings.Trim(relativeUrlPath, "/")
}
