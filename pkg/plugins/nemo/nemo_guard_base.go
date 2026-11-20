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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	errcommon "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/error"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	nemoStatusSuccess  = "success"
	nemoStatusModified = "modified"
	nemoStatusBlocked  = "blocked"
	nemoStatusError    = "error"

	defaultTimeoutSec    = 360
	maxNemoResponseBytes = 1 << 20 // 1 MiB

	// genericBlockedDetail is the error text callNemoGuard returns for a block
	// that did not include a refusal string. Plugins replace it with a
	// direction-specific client message.
	genericBlockedDetail = "blocked by NeMo guardrails"

	// maxRefusalBytes caps the refusal copied into the client error so a
	// hostile or huge checks payload cannot become an unbounded 403 body.
	maxRefusalBytes = 4096
)

// nemoGuardConfig is the configuration for nemo guard plugins.
type nemoGuardConfig struct {
	NemoURL        string `json:"nemoURL"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// nemoResponse is NeMo's JSON response from /v1/guardrail/checks.
// Possible top-level statuses: "success" (all rails passed), "blocked" (at least
// one rail blocked), "error" (NeMo internal processing error).
// All statuses are returned with HTTP 200; the decision is in the JSON body.
// Content, when present on a block, is the configured refusal (Colang
// "bot refuse to respond" / RailsResult.content).
type nemoResponse struct {
	Status         string                         `json:"status"`
	Content        string                         `json:"content"`
	RailsStatus    map[string]nemoRailStatusEntry `json:"rails_status"`
	GuardrailsData map[string]any                 `json:"guardrails_data"`
}

type nemoRailStatusEntry struct {
	Status  string `json:"status"`
	Content string `json:"content"`
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
	parsed, err := url.ParseRequestURI(nemoURL)
	if err != nil {
		return nil, fmt.Errorf("invalid nemoURL: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("nemoURL must use https:// scheme, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("nemoURL must include a host")
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSec * time.Second
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}

	return &nemoGuardBase{
		nemoURL: nemoURL,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: tr,
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
	case nemoStatusSuccess:
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
		if msg := refusalText(nemoResp); msg != "" {
			return errcommon.Forbidden, errors.New(msg)
		}
		return errcommon.Forbidden, errors.New(genericBlockedDetail)

	case nemoStatusError:
		errMsg, _ := nemoResp.GuardrailsData["error"].(string)
		if errMsg == "" {
			errMsg = "unspecified error"
		}
		logger.Error(nil, "NeMo guardrails returned error status", "nemoError", errMsg)
		return errcommon.ServiceUnavailable, fmt.Errorf("nemo guardrails error: %s", errMsg)

	default:
		logger.Error(nil, "unknown NeMo guardrails status (fail-closed)", "status", nemoResp.Status)
		return errcommon.Internal, fmt.Errorf("unknown NeMo guardrails status %q", nemoResp.Status)
	}
}

// refusalText returns the user-visible refusal from a blocked checks response.
// The checks endpoint's decision is the status field; content is optional and,
// when a deployment includes it, is the Colang refusal rather than the user text.
func refusalText(resp nemoResponse) string {
	if msg := strings.TrimSpace(resp.Content); msg != "" {
		return truncateRefusal(msg)
	}
	for _, key := range []string{"content", "refusal", "response"} {
		raw, ok := resp.GuardrailsData[key]
		if !ok {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			continue
		}
		if msg := strings.TrimSpace(text); msg != "" {
			return truncateRefusal(msg)
		}
	}

	keys := make([]string, 0, len(resp.RailsStatus))
	for key, entry := range resp.RailsStatus {
		blocked := strings.EqualFold(strings.TrimSpace(entry.Status), nemoStatusBlocked)
		if blocked && strings.TrimSpace(entry.Content) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return truncateRefusal(strings.TrimSpace(resp.RailsStatus[keys[0]].Content))
	}
	return ""
}

// clientBlockedMessage is the text placed on the Forbidden error. A checks
// response without a refusal keeps the direction-specific fallback so clients
// still receive a stable sentence.
func clientBlockedMessage(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" || msg == genericBlockedDetail {
		return fallback
	}
	return msg
}

func truncateRefusal(msg string) string {
	if len(msg) <= maxRefusalBytes {
		return msg
	}
	cut := maxRefusalBytes
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	if cut == 0 {
		return ""
	}
	return msg[:cut]
}
