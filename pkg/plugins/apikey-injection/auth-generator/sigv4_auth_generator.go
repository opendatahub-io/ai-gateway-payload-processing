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

package authgenerator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

const (
	awsAccessKeyField    = "aws-access-key-id"
	awsSecretKeyField    = "aws-secret-access-key"
	awsSessionTokenField = "aws-session-token"

	bodyField     = "request_body"
	endpointField = "endpoint"
	pathField     = "path"
	regionField   = "region"
	serviceField  = "service"
)

// compile-time interface check
var _ AuthHeadersGenerator = &SigV4AuthGenerator{}

func NewSigV4AuthGenerator() *SigV4AuthGenerator {
	return &SigV4AuthGenerator{}
}

// SigV4AuthGenerator generates AWS Signature Version 4 authentication headers.
type SigV4AuthGenerator struct{}

// ExtractRequestData pulls the request body, endpoint, path, region, and service
// from CycleState and the InferenceRequest. Region falls back to hostname extraction
// if not set in config; endpoint, path and service are mandatory. The returned map is merged into
// credentialsData before GenerateAuthHeaders is called.
func (g *SigV4AuthGenerator) ExtractRequestData(cycleState *framework.CycleState, request *framework.InferenceRequest) (map[string]string, error) {
	// json.Marshal produces deterministic output (sorted map keys), ensuring the
	// signed body matches what the framework sends upstream. If the framework ever
	// changes its serializer, this assumption must be revisited.
	bodyBytes, err := json.Marshal(request.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	endpoint, err := framework.ReadCycleStateKey[string](cycleState, state.EndpointKey)
	if err != nil || endpoint == "" {
		return nil, fmt.Errorf("missing or empty endpoint in CycleState (key %q)", state.EndpointKey)
	}

	path := request.Headers[":path"]
	if path == "" {
		return nil, fmt.Errorf("missing :path pseudo-header in request")
	}

	config, _ := framework.ReadCycleStateKey[map[string]string](cycleState, state.ModelConfigKey)

	region := config["region"]
	if region == "" {
		var err error
		region, err = regionFromEndpoint(endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve region: %w", err)
		}
	}

	service := config["service"]
	if service == "" {
		return nil, fmt.Errorf("missing \"service\" in ExternalProvider.spec.config -- set the AWS service name (e.g. \"bedrock\", \"sagemaker\")")
	}

	return map[string]string{
		bodyField:     string(bodyBytes),
		endpointField: endpoint,
		pathField:     path,
		regionField:   region,
		serviceField:  service,
	}, nil
}

// GenerateAuthHeaders computes a SigV4 signature and returns the required AWS auth headers.
// All runtime fields (body, endpoint, path, region, service) are guaranteed by ExtractRequestData.
// Only "aws-access-key-id" and "aws-secret-access-key" come from the Secret and need validation here.
func (g *SigV4AuthGenerator) GenerateAuthHeaders(credentialsData map[string]string) (map[string]string, error) {
	accessKey := credentialsData[awsAccessKeyField]
	if accessKey == "" {
		return nil, fmt.Errorf("credentials missing required field %s", awsAccessKeyField)
	}
	secretKey := credentialsData[awsSecretKeyField]
	if secretKey == "" {
		return nil, fmt.Errorf("credentials missing required field %s", awsSecretKeyField)
	}

	creds := aws.Credentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		SessionToken:    credentialsData[awsSessionTokenField],
		Source:          "ExternalModelSecret",
	}

	body := credentialsData[bodyField]
	bodyHash := sha256Hex([]byte(body))

	endpoint := credentialsData[endpointField]
	path := credentialsData[pathField]
	region := credentialsData[regionField]
	service := credentialsData[serviceField]

	req, err := http.NewRequest(http.MethodPost, "https://"+endpoint+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP request for signing: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	signer := v4.NewSigner()
	if err := signer.SignHTTP(context.Background(), creds, req, bodyHash, service, region, time.Now()); err != nil {
		return nil, fmt.Errorf("SigV4 signing failed: %w", err)
	}

	headers := map[string]string{
		"Authorization":        req.Header.Get("Authorization"),
		"X-Amz-Date":           req.Header.Get("X-Amz-Date"),
		"X-Amz-Content-Sha256": bodyHash,
	}
	if creds.SessionToken != "" {
		headers["X-Amz-Security-Token"] = creds.SessionToken
	}

	return headers, nil
}

// regionFromEndpoint extracts the AWS region from a standard Bedrock endpoint hostname.
// Only accepts the canonical format: service.region.amazonaws.com (exactly 4 labels).
// VPC endpoints, custom endpoints, or any non-standard format will return an error
// instructing the user to set "region" explicitly in ExternalProvider.spec.config.
func regionFromEndpoint(endpoint string) (string, error) {
	if strings.Contains(endpoint, "://") {
		return "", fmt.Errorf("endpoint must be a bare hostname without scheme: %q", endpoint)
	}
	parts := strings.Split(endpoint, ".")
	if len(parts) != 4 || parts[2] != "amazonaws" || parts[3] != "com" {
		return "", fmt.Errorf(
			"cannot extract region from non-standard endpoint %q -- set \"region\" explicitly in ExternalProvider.spec.config",
			endpoint,
		)
	}
	return parts[1], nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
