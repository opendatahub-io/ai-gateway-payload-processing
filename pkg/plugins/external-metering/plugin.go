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

package external_metering

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/log"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	ExternalMeteringPluginType          = "external-metering"
	ExternalMeteringStreamingPluginType = "external-metering-streaming"

	defaultTimeoutSec = 5
	defaultFeatureKey = "inference-tokens"
	defaultSource     = "maas-gateway"
)

// compile-time interface assertions
var _ requesthandling.RequestProcessor = &ExternalMeteringPlugin{}
var _ requesthandling.ResponseProcessor = &ExternalMeteringPlugin{}
var _ requesthandling.RequestProcessor = &ExternalMeteringStreamingPlugin{}
var _ requesthandling.ResponseChunkProcessor = &ExternalMeteringStreamingPlugin{}

// --- Shared config and construction ---

type externalMeteringConfig struct {
	MeteringURL    string `json:"meteringURL"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	FeatureKey     string `json:"featureKey,omitempty"`
	Source         string `json:"source,omitempty"`
	FailOpen       *bool  `json:"failOpen,omitempty"`
}

type meteringBase struct {
	typedName  plugin.TypedName
	client     *meteringClient
	featureKey string
	source     string
	failOpen   bool
}

func parseConfig(pluginType string, rawParameters json.RawMessage) (*meteringBase, error) {
	defaultFailOpen := true
	config := externalMeteringConfig{
		TimeoutSeconds: defaultTimeoutSec,
		FeatureKey:     defaultFeatureKey,
		Source:         defaultSource,
		FailOpen:       &defaultFailOpen,
	}

	if len(rawParameters) > 0 {
		if err := json.Unmarshal(rawParameters, &config); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of '%s' plugin - %w", pluginType, err)
		}
	}

	if config.MeteringURL == "" {
		return nil, fmt.Errorf("'meteringURL' is required for '%s' plugin", pluginType)
	}

	if config.FeatureKey == "" {
		config.FeatureKey = defaultFeatureKey
	}
	if config.Source == "" {
		config.Source = defaultSource
	}

	return &meteringBase{
		typedName: plugin.TypedName{
			Type: pluginType,
			Name: pluginType,
		},
		client:     newMeteringClient(config.MeteringURL, config.TimeoutSeconds),
		featureKey: config.FeatureKey,
		source:     config.Source,
		failOpen:   config.FailOpen == nil || *config.FailOpen,
	}, nil
}

// processRequest is the shared request-side logic: read identity from maas-headers
// CycleState (set by maas-headers-guard), check balance, strip Accept-Encoding.
func (b *meteringBase) processRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	logger := log.FromContext(ctx)

	// Read identity from maas-headers CycleState (set by maas-headers-guard plugin).
	// Falls back to reading directly from request headers for backward compatibility.
	var username, group, subscription string
	if maasHeaders, err := plugin.ReadCycleStateKey[map[string]string](cycleState, "maas-headers"); err == nil {
		username = maasHeaders["x-maas-username"]
		if username == "" {
			username = maasHeaders["X-MaaS-Username"]
		}
		group = maasHeaders["x-maas-group"]
		if group == "" {
			group = maasHeaders["X-MaaS-Group"]
		}
		subscription = maasHeaders["x-maas-subscription"]
		if subscription == "" {
			subscription = maasHeaders["X-MaaS-Subscription"]
		}
	}
	if username == "" {
		username = request.Headers["x-maas-username"]
	}
	if username == "" {
		username = request.Headers["x-maas-subscription"]
	}
	if username == "" {
		logger.V(logutil.VERBOSE).Info("no username or subscription header found, skipping metering")
		return nil
	}

	if subscription == "" {
		subscription = request.Headers["x-maas-subscription"]
	}
	if group == "" {
		group = request.Headers["x-maas-group"]
	}
	if group == "" {
		group = subscription
	}

	model, _ := request.Body["model"].(string)

	cycleState.Write(state.MeteringUsernameKey, username)
	cycleState.Write(state.MeteringGroupKey, group)
	cycleState.Write(state.MeteringSubscriptionKey, subscription)
	cycleState.Write(state.MeteringModelKey, model)
	cycleState.Write(state.MeteringRequestTimeKey, time.Now())

	result, err := b.client.checkBalance(ctx, username, b.featureKey, model)
	if err != nil {
		if b.failOpen {
			logger.Error(err, "metering balance check failed (fail-open), allowing request")
			return nil
		}
		return errcommon.Error{Code: errcommon.ServiceUnavailable, Msg: fmt.Sprintf("metering system unavailable: %v", err)}
	}

	if !result.HasAccess {
		logger.Info("request blocked by metering", "customer", username, "balance", result.Balance)
		return errcommon.Error{Code: errcommon.ResourceExhausted, Msg: "token budget exhausted"}
	}

	logger.V(logutil.VERBOSE).Info("metering check passed", "customer", username, "balance", result.Balance)

	// Strip Accept-Encoding so the upstream sends uncompressed SSE responses.
	// The chunk processor needs plain text to extract usage data from streaming responses.
	request.RemoveHeader("accept-encoding")

	// Note: stream_options.include_usage injection is handled by the
	// stream-usage-enforcer plugin (PR #364/#367), not here.

	return nil
}

// reportUsageEvent builds and sends a usage event to the metering service.
func (b *meteringBase) reportUsageEvent(ctx context.Context, cycleState *plugin.CycleState, usage map[string]any) {
	logger := log.FromContext(ctx)

	username, _ := plugin.ReadCycleStateKey[string](cycleState, state.MeteringUsernameKey)
	group, _ := plugin.ReadCycleStateKey[string](cycleState, state.MeteringGroupKey)
	subscription, _ := plugin.ReadCycleStateKey[string](cycleState, state.MeteringSubscriptionKey)
	model, _ := plugin.ReadCycleStateKey[string](cycleState, state.MeteringModelKey)
	provider, _ := plugin.ReadCycleStateKey[string](cycleState, state.ProviderKey)

	promptTokens, completionTokens, totalTokens := extractTokenCounts(usage)

	cachedInputTokens := 0
	cacheCreationTokens := 0
	reasoningTokens := 0

	if v := toInt(usage["cache_read_input_tokens"]); v > 0 {
		cachedInputTokens = v
	}
	if v := toInt(usage["cache_creation_input_tokens"]); v > 0 {
		cacheCreationTokens = v
	}
	if cachedInputTokens == 0 {
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			cachedInputTokens = toInt(details["cached_tokens"])
		}
	}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		reasoningTokens = toInt(details["reasoning_tokens"])
	}

	var durationMs int64
	if reqTime, err := plugin.ReadCycleStateKey[time.Time](cycleState, state.MeteringRequestTimeKey); err == nil {
		durationMs = time.Since(reqTime).Milliseconds()
	}

	event := map[string]any{
		"specversion":     "1.0",
		"id":              fmt.Sprintf("evt-%s", uuid.New().String()),
		"source":          b.source,
		"type":            "inference.tokens.used",
		"subject":         username,
		"time":            time.Now().UTC().Format(time.RFC3339),
		"datacontenttype": "application/json",
		"data": map[string]any{
			"user":                  username,
			"group":                 group,
			"subscription":          subscription,
			"provider":              provider,
			"model":                 model,
			"prompt_tokens":         promptTokens,
			"completion_tokens":     completionTokens,
			"total_tokens":          totalTokens,
			"cached_input_tokens":   cachedInputTokens,
			"cache_creation_tokens": cacheCreationTokens,
			"reasoning_tokens":      reasoningTokens,
			"duration_ms":           durationMs,
		},
	}

	eventJSON, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		logger.Error(marshalErr, "failed to marshal usage event")
		return
	}

	if reportErr := b.client.reportUsage(ctx, eventJSON); reportErr != nil {
		logger.Error(reportErr, "failed to report usage to metering system")
	} else {
		logger.V(logutil.VERBOSE).Info("usage reported", "customer", username, "model", model, "tokens", totalTokens)
	}
}

// --- ExternalMeteringPlugin (ResponseProcessor — full body) ---

// ExternalMeteringPlugin implements RequestProcessor and ResponseProcessor.
// Use in profiles that buffer the full response body (e.g., with api-translation).
type ExternalMeteringPlugin struct {
	meteringBase
}

func ExternalMeteringFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	base, err := parseConfig(ExternalMeteringPluginType, rawParameters)
	if err != nil {
		return nil, err
	}
	p := &ExternalMeteringPlugin{meteringBase: *base}
	return p.WithName(name), nil
}

func (p *ExternalMeteringPlugin) TypedName() plugin.TypedName { return p.typedName }

func (p *ExternalMeteringPlugin) WithName(name string) *ExternalMeteringPlugin {
	p.typedName.Name = name
	return p
}

func (p *ExternalMeteringPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	return p.processRequest(ctx, cycleState, request)
}

func (p *ExternalMeteringPlugin) ProcessResponse(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse) error {
	username, err := plugin.ReadCycleStateKey[string](cycleState, state.MeteringUsernameKey)
	if err != nil || username == "" {
		return nil
	}

	usage, ok := response.Body["usage"].(map[string]any)
	if !ok {
		log.FromContext(ctx).V(logutil.VERBOSE).Info("no usage data in response, skipping metering report")
		return nil
	}

	p.reportUsageEvent(ctx, cycleState, usage)
	return nil
}

// --- ExternalMeteringStreamingPlugin (RequestProcessor only — for streaming profiles) ---

// ExternalMeteringStreamingPlugin implements RequestProcessor only.
// Use in profiles that stream response chunks (no buffering).
// Response-side chunk processing (ResponseChunkProcessor) will be added
// after the upstream framework merges the ResponseChunkProcessor interface (PR #169).
type ExternalMeteringStreamingPlugin struct {
	meteringBase
}

func ExternalMeteringStreamingFactory(name string, rawParameters json.RawMessage, _ plugin.Handle) (plugin.Plugin, error) {
	base, err := parseConfig(ExternalMeteringStreamingPluginType, rawParameters)
	if err != nil {
		return nil, err
	}
	p := &ExternalMeteringStreamingPlugin{meteringBase: *base}
	return p.WithName(name), nil
}

func (p *ExternalMeteringStreamingPlugin) TypedName() plugin.TypedName { return p.typedName }

func (p *ExternalMeteringStreamingPlugin) WithName(name string) *ExternalMeteringStreamingPlugin {
	p.typedName.Name = name
	return p
}

func (p *ExternalMeteringStreamingPlugin) ProcessRequest(ctx context.Context, cycleState *plugin.CycleState, request *requesthandling.InferenceRequest) error {
	return p.processRequest(ctx, cycleState, request)
}

func (p *ExternalMeteringStreamingPlugin) ProcessResponseChunk(ctx context.Context, cycleState *plugin.CycleState, response *requesthandling.InferenceResponse, chunk string, isFinal bool) error {
	logger := log.FromContext(ctx)

	username, _ := plugin.ReadCycleStateKey[string](cycleState, state.MeteringUsernameKey)
	if username == "" {
		return nil
	}

	preview := chunk
	if len(preview) > 300 {
		preview = preview[:300] + "..."
	}
	logger.Info("chunk received", "length", len(chunk), "isFinal", isFinal, "preview", preview)

	if chunk != "" {
		usage := extractUsageFromChunk(chunk)
		if usage != nil {
			logger.Info("usage found in chunk", "usage", usage)
			cycleState.Write(state.MeteringLastUsageKey, usage)
		}
	}

	if isFinal {
		lastUsage, err := plugin.ReadCycleStateKey[map[string]any](cycleState, state.MeteringLastUsageKey)
		if err != nil || lastUsage == nil {
			logger.Info("no usage data found in streaming response")
			return nil
		}
		p.reportUsageEvent(ctx, cycleState, lastUsage)
	}

	return nil
}

// extractUsageFromChunk parses SSE or JSON chunks to find usage data.
func extractUsageFromChunk(chunk string) map[string]any {
	lines := splitSSELines(chunk)
	for _, line := range lines {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			continue
		}
		if usage, ok := parsed["usage"].(map[string]any); ok {
			return usage
		}
		if msg, ok := parsed["message"].(map[string]any); ok {
			if usage, ok := msg["usage"].(map[string]any); ok {
				return usage
			}
		}
		if delta, ok := parsed["delta"].(map[string]any); ok {
			if usage, ok := delta["usage"].(map[string]any); ok {
				return usage
			}
		}
	}
	return nil
}

func splitSSELines(chunk string) []string {
	var dataLines []string
	for _, line := range strings.Split(chunk, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)
			dataLines = append(dataLines, data)
		} else if len(line) > 0 && line[0] == '{' {
			dataLines = append(dataLines, strings.TrimSpace(line))
		}
	}
	return dataLines
}

// --- Shared helpers ---

func extractTokenCounts(usage map[string]any) (prompt, completion, total int) {
	prompt = toInt(usage["prompt_tokens"])
	completion = toInt(usage["completion_tokens"])
	total = toInt(usage["total_tokens"])

	if prompt == 0 && completion == 0 {
		prompt = toInt(usage["input_tokens"])
		completion = toInt(usage["output_tokens"])
	}

	if total == 0 {
		total = prompt + completion
	}
	return
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}
