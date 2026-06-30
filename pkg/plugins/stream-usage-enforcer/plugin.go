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

package stream_usage_enforcer

import (
	"context"
	"encoding/json"

	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const PluginType = "stream-usage-enforcer"

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

	stream, ok := request.Body["stream"].(bool)
	if !ok || !stream {
		return nil
	}

	inputFormat, _ := plugin.ReadCycleStateKey[apiformat.APIFormat](cycleState, state.InputAPIFormatKey)
	if inputFormat != "" && inputFormat != apiformat.OpenAIChatCompletions {
		logger.Info("skipping stream_options enforcement for non-OpenAI format", "inputFormat", inputFormat)
		return nil
	}

	streamOptions, _ := request.Body["stream_options"].(map[string]any)
	if streamOptions == nil {
		streamOptions = map[string]any{}
	}

	if existing, ok := streamOptions["include_usage"].(bool); ok && existing {
		logger.Info("stream_options.include_usage already set")
		return nil
	}

	streamOptions["include_usage"] = true
	request.SetBodyField("stream_options", streamOptions)

	logger.Info("enforced stream_options.include_usage=true")
	return nil
}
