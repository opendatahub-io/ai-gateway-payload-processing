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

package maas_headers_guard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	PluginType = "maas-headers-guard"

	headerPrefix = "x-maas-"

	// Routing headers set by body-field-to-header and model-provider-resolver.
	// See config.StripRoutingHeaders for when the guard removes them.
	modelNameHeader        = "x-gateway-model-name"
	selectedProviderHeader = "x-ipp-selected-provider"

	// MaaSHeadersKey is the CycleState key where captured x-maas-* headers are stored
	// as a map[string]string. Downstream plugins read from this instead of request headers.
	MaaSHeadersKey = "maas-headers"
)

var _ requesthandling.RequestProcessor = &Plugin{}

type config struct {
	// StripRoutingHeaders removes x-gateway-model-name and
	// x-ipp-selected-provider arriving on the request. Enable it only where
	// the guard runs before body-field-to-header and model-provider-resolver
	// in the same chain, which set the headers again from the body. A
	// post-auth chain behind a separate pre-auth stage (the MaaS layout) must
	// keep them: the HTTPRoutes match on them, and the body's model was
	// already rewritten to the provider's targetModel.
	StripRoutingHeaders bool `json:"stripRoutingHeaders"`
}

type Plugin struct {
	typedName           plugin.TypedName
	stripRoutingHeaders bool
}

func Factory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	var cfg config
	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of '%s' plugin - %w", PluginType, err)
		}
	}
	return (&Plugin{
		typedName:           plugin.TypedName{Type: PluginType, Name: PluginType},
		stripRoutingHeaders: cfg.StripRoutingHeaders,
	}).WithName(name), nil
}

func (p *Plugin) WithName(name string) *Plugin {
	p.typedName.Name = name
	return p
}

func (p *Plugin) TypedName() plugin.TypedName { return p.typedName }

func (p *Plugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx).V(logutil.VERBOSE)

	captured := make(map[string]string)
	for key, value := range request.Headers {
		if !strings.HasPrefix(strings.ToLower(key), headerPrefix) {
			continue
		}
		captured[key] = value
		request.RemoveHeader(key)
		logger.Info("internal header captured and stripped", "header", key)
	}

	// Capture then strip X-Origin-Cluster. Guard runs before
	// model-provider-resolver, so a bare strip would drop the hop-2 origin
	// before the loop check. CycleState preserves it; stripping keeps it off
	// third-party upstreams until the resolver restores it on remote-maas hops.
	for key, value := range request.Headers {
		if !strings.EqualFold(key, state.OriginClusterHeader) {
			continue
		}
		cycleState.Write(state.OriginClusterKey, value)
		request.RemoveHeader(key)
		logger.Info("origin cluster header captured and stripped", "header", key)
	}

	// Strip gateway-internal routing headers arriving on the wire. In a
	// single chain they are derived from the request body by the plugins
	// after this one; a value that arrives from a client, or from the
	// previous cluster on a cross-cluster hop, describes someone else's
	// routing decision and would make model-provider-resolver look up the
	// wrong model.
	if p.stripRoutingHeaders {
		for key := range request.Headers {
			if strings.EqualFold(key, modelNameHeader) || strings.EqualFold(key, selectedProviderHeader) {
				request.RemoveHeader(key)
				logger.Info("routing header stripped", "header", key)
			}
		}
	}

	// Strip client auth credentials so they never reach the upstream model.
	// For ExternalModels, apikey-injection re-adds the provider credential later.
	// For internal models (LLMISvc), no credential is needed (mTLS handles auth).
	request.RemoveHeader("authorization")
	request.RemoveHeader("x-api-key")

	cycleState.Write(MaaSHeadersKey, captured)

	return nil
}
