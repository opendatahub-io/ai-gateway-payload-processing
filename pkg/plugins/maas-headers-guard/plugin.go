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
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

const (
	PluginType = "maas-headers-guard"

	headerPrefix = "x-maas-"

	// MaaSHeadersKey is the CycleState key where captured x-maas-* headers are stored
	// as a map[string]string. Downstream plugins read from this instead of request headers.
	MaaSHeadersKey = "maas-headers"
)

var _ requesthandling.RequestProcessor = &Plugin{}

type Plugin struct {
	typedName plugin.TypedName
}

func Factory(name string, _ json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	return (&Plugin{
		typedName: plugin.TypedName{Type: PluginType, Name: PluginType},
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

	// Strip client auth credentials so they never reach the upstream model.
	// For ExternalModels, apikey-injection re-adds the provider credential later.
	// For internal models (LLMISvc), no credential is needed (mTLS handles auth).
	request.RemoveHeader("authorization")
	request.RemoveHeader("x-api-key")

	cycleState.Write(MaaSHeadersKey, captured)

	return nil
}
