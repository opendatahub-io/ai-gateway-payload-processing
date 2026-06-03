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

package nemo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
)

const (
	nemoStatusPassed   = "passed"
	nemoStatusModified = "modified"
	nemoStatusBlocked  = "blocked"

	defaultTimeoutSec    = 360
	maxNemoResponseBytes = 1 << 20 // 1 MiB
)

// nemoGuardConfig is the configuration for nemo guard plugins.
type nemoGuardConfig struct {
	NemoURL        string `json:"nemoURL"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// nemoGuardResult holds the normalized outcome of a callNemoGuard invocation.
type nemoGuardResult struct {
	Action        string   // nemoStatusPassed, nemoStatusModified, or nemoStatusBlocked
	ModifiedTexts []string // per-message redacted texts when Action is nemoStatusModified
}

// nemoResponse is NeMo's JSON response from /v1/guardrail/checks.
type nemoResponse struct {
	Status         string                         `json:"status"`
	RailsStatus    map[string]nemoRailStatusEntry `json:"rails_status"`
	GuardrailsData json.RawMessage                `json:"guardrails_data"`
}

type nemoRailStatusEntry struct {
	Status string `json:"status"`
}

// nemoGuardBase holds the shared fields and HTTP logic for nemo guard plugins.
type nemoGuardBase struct {
	nemoURL    string
	httpClient *http.Client
}

func newNemoGuardBase(nemoURL string, timeoutSeconds int) (*nemoGuardBase, error) {
	if nemoURL == "" {
		return nil, errors.New("nemoURL is required")
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSec * time.Second
	}

	return &nemoGuardBase{
		nemoURL: nemoURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// callNemoGuard POSTs the payload to the configured NeMo endpoint, parses the response,
// and returns a nemoGuardResult with the normalized action. Returns an errcommon.Error
// if NeMo is unreachable or the response cannot be parsed.
func (b *nemoGuardBase) callNemoGuard(ctx context.Context, payload []byte, phase string) (*nemoGuardResult, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	log.FromContext(ctx).V(logutil.VERBOSE).Info("calling NeMo guardrails")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.nemoURL, bytes.NewReader(payload))
	if err != nil {
		logger.Error(err, "failed to create NeMo request")
		return nil, errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("failed to create nemo request: %v", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		logger.Error(err, "NeMo guardrail call failed")
		return nil, errcommon.Error{Code: errcommon.ServiceUnavailable, Msg: fmt.Sprintf("nemo call failed: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.Error(nil, "NeMo guardrail unexpected status", "statusCode", resp.StatusCode)
		return nil, errcommon.Error{Code: errcommon.ServiceUnavailable, Msg: fmt.Sprintf("unexpected status %d", resp.StatusCode)}
	}

	limited := io.LimitReader(resp.Body, maxNemoResponseBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		logger.Error(err, "failed to read NeMo response")
		return nil, errcommon.Error{Code: errcommon.ServiceUnavailable, Msg: fmt.Sprintf("failed to read nemo response: %v", err)}
	}

	var nemoResp nemoResponse
	if err := json.Unmarshal(body, &nemoResp); err != nil {
		logger.Error(err, "failed to decode NeMo response")
		return nil, errcommon.Error{Code: errcommon.ServiceUnavailable, Msg: fmt.Sprintf("failed to decode nemo response: %v", err)}
	}

	status := strings.TrimSpace(nemoResp.Status)

	switch status {
	case nemoStatusPassed:
		logger.Info("allowed by NeMo guardrails")
		return &nemoGuardResult{Action: nemoStatusPassed}, nil

	case nemoStatusModified:
		modifiedMessages := extractModifiedTexts(nemoResp.GuardrailsData)
		if len(modifiedMessages) == 0 {
			logger.Error(nil, "NeMo returned modified status but no redacted content found (fail-closed)")
			return nil, errcommon.Error{Code: errcommon.Internal, Msg: "NeMo returned modified status but no redacted content found"}
		}
		logger.Info("content modified by NeMo guardrails", "modifiedMessages", len(modifiedMessages))
		return &nemoGuardResult{Action: nemoStatusModified, ModifiedTexts: modifiedMessages}, nil

	case nemoStatusBlocked:
		railsParts := make([]string, 0, len(nemoResp.RailsStatus))
		for key, value := range nemoResp.RailsStatus {
			railsParts = append(railsParts, fmt.Sprintf("%s: %s", key, value.Status))
		}
		railsStatus := fmt.Sprintf("[ %s ]", strings.Join(railsParts, " "))
		log.FromContext(ctx).Info("blocked by NeMo guardrails", "railsStatus", railsStatus)
		return &nemoGuardResult{Action: nemoStatusBlocked}, errcommon.Error{Code: errcommon.Forbidden, Msg: fmt.Sprintf("%s blocked by NeMo guardrails", phase)}

	default:
		logger.Error(nil, "unknown NeMo guardrails status (fail-closed)", "status", nemoResp.Status)
		return nil, errcommon.Error{Code: errcommon.Internal, Msg: fmt.Sprintf("unknown NeMo guardrails status %q", nemoResp.Status)}
	}
}

// extractModifiedTexts collects all non-empty string return values from
// guardrails_data.log.activated_rails. Each collected string corresponds to a
// message in the original payload. Non-string return values (e.g. boolean from
// detect actions) and empty strings are skipped. Returns nil if no results are found.
func extractModifiedTexts(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	logData, _ := data["log"].(map[string]any)
	rails, _ := logData["activated_rails"].([]any)
	if len(rails) == 0 {
		return nil
	}

	var modifiedTexts []string
	for _, rail := range rails {
		railMap, _ := rail.(map[string]any)
		actions, _ := railMap["executed_actions"].([]any)
		for _, action := range actions {
			actionMap, _ := action.(map[string]any)
			returnValue, ok := actionMap["return_value"].(string)
			if !ok || returnValue == "" {
				continue
			}
			modifiedTexts = append(modifiedTexts, returnValue)
			break
		}
	}
	return modifiedTexts
}
