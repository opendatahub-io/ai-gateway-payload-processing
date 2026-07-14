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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"time"
)

const maxResponseBytes = 1 << 20 // 1 MiB

type entitlementValue struct {
	HasAccess bool    `json:"hasAccess"`
	Balance   float64 `json:"balance"`
	Usage     float64 `json:"usage"`
	Overage   float64 `json:"overage"`
}

type meteringClient struct {
	httpClient *http.Client
	baseURL    string
}

func newMeteringClient(baseURL string, timeoutSeconds int) *meteringClient {
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &meteringClient{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
	}
}

func (c *meteringClient) checkBalance(ctx context.Context, customerID, featureKey, model string) (*entitlementValue, error) {
	url := fmt.Sprintf("%s/api/v1/customers/%s/entitlements/%s/value?model=%s",
		c.baseURL, neturl.PathEscape(customerID), neturl.PathEscape(featureKey), neturl.QueryEscape(model))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create balance check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("balance check call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("balance check returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read balance check response: %w", err)
	}

	var result entitlementValue
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode balance check response: %w", err)
	}

	return &result, nil
}

func (c *meteringClient) reportUsage(ctx context.Context, event []byte) error {
	url := fmt.Sprintf("%s/api/v1/events", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(event))
	if err != nil {
		return fmt.Errorf("failed to create usage report request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("usage report call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("usage report returned status %d", resp.StatusCode)
	}

	return nil
}
