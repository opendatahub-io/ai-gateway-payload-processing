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
	errcommon "sigs.k8s.io/gateway-api-inference-extension/pkg/common/error"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
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

// nemoResponse is NeMo's JSON response from /v1/guardrail/checks.
type nemoResponse struct {
	Status      string                         `json:"status"`
	RailsStatus map[string]nemoRailStatusEntry `json:"rails_status"`
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

// callNemoGuard POSTs the payload to the NeMo guardrail checks endpoint, parses the
// response, and returns an error with the corresponding error code if the content is
// blocked or NeMo is unreachable. The caller is responsible for constructing the
// client-facing errcommon.Error from the returned values.
func (b *nemoGuardBase) callNemoGuard(ctx context.Context, payload []byte) (string, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	log.FromContext(ctx).V(logutil.VERBOSE).Info("calling NeMo guardrails")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.nemoURL, bytes.NewReader(payload))
	if err != nil {
		logger.Error(err, "failed to create NeMo request")
		return errcommon.Internal, fmt.Errorf("failed to create nemo request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		logger.Error(err, "NeMo guardrail call failed")
		return errcommon.ServiceUnavailable, fmt.Errorf("nemo call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.Error(nil, "NeMo guardrail unexpected status", "statusCode", resp.StatusCode)
		return errcommon.ServiceUnavailable, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxNemoResponseBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		logger.Error(err, "failed to read NeMo response")
		return errcommon.ServiceUnavailable, fmt.Errorf("failed to read nemo response: %w", err)
	}

	var nemoResp nemoResponse
	if err := json.Unmarshal(body, &nemoResp); err != nil {
		logger.Error(err, "failed to decode NeMo response")
		return errcommon.ServiceUnavailable, fmt.Errorf("failed to decode nemo response: %w", err)
	}

	status := strings.TrimSpace(nemoResp.Status)

	switch status {
	case nemoStatusPassed:
		logger.Info("allowed by NeMo guardrails")
		return "", nil

	case nemoStatusModified:
		// TODO: support redaction by applying NeMo's masked content to the request/response body.
		logger.Info("content modified by NeMo guardrails (redaction not yet applied)")
		return "", nil

	case nemoStatusBlocked:
		railsParts := make([]string, 0, len(nemoResp.RailsStatus))
		for key, value := range nemoResp.RailsStatus {
			railsParts = append(railsParts, fmt.Sprintf("%s: %s", key, value.Status))
		}
		railsStatus := fmt.Sprintf("[ %s ]", strings.Join(railsParts, " "))
		log.FromContext(ctx).Info("blocked by NeMo guardrails", "railsStatus", railsStatus)
		return errcommon.Forbidden, fmt.Errorf("blocked by NeMo guardrails")

	default:
		logger.Error(nil, "unknown NeMo guardrails status (fail-closed)", "status", nemoResp.Status)
		return errcommon.Internal, fmt.Errorf("unknown NeMo guardrails status %q", nemoResp.Status)
	}
}
