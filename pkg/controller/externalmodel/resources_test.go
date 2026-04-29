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

package externalmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestCommonLabels(t *testing.T) {
	labels := commonLabels("gpt4")
	assert.Equal(t, managedByValue, labels[labelManagedBy])
	assert.Equal(t, "gpt4", labels[labelExternalModel])
	assert.Len(t, labels, 2)
}

func TestBuildHTTPRoute_SingleProvider(t *testing.T) {
	providers := []resolvedProvider{
		{Name: "my-openai", Endpoint: "api.openai.com"},
	}
	hr := buildHTTPRoute("gpt4", "models", providers, 443,
		"default-gateway", "openshift-ingress", "300s", commonLabels("gpt4"))

	assert.Equal(t, "gpt4", hr.Name)
	assert.Equal(t, "models", hr.Namespace)

	require.Len(t, hr.Spec.ParentRefs, 1)
	assert.Equal(t, "default-gateway", string(hr.Spec.ParentRefs[0].Name))

	// 2 rules: path-based + 1 provider header
	require.Len(t, hr.Spec.Rules, 2)

	// Rule 1: path-based
	assert.Equal(t, "/models/gpt4", *hr.Spec.Rules[0].Matches[0].Path.Value)
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[0].BackendRefs[0].Name))

	// Rule 2: X-Selected-Provider header match
	assert.Equal(t, "X-Selected-Provider", string(hr.Spec.Rules[1].Matches[0].Headers[0].Name))
	assert.Equal(t, "my-openai", hr.Spec.Rules[1].Matches[0].Headers[0].Value)
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[1].BackendRefs[0].Name))

	// Host header for TLS SNI
	for _, rule := range hr.Spec.Rules {
		require.Len(t, rule.Filters, 1)
		assert.Equal(t, gatewayapiv1.HTTPRouteFilterRequestHeaderModifier, rule.Filters[0].Type)
		assert.Equal(t, "Host", string(rule.Filters[0].RequestHeaderModifier.Set[0].Name))
		assert.Equal(t, "api.openai.com", rule.Filters[0].RequestHeaderModifier.Set[0].Value)
	}
}

func TestBuildHTTPRoute_MultipleProviders(t *testing.T) {
	providers := []resolvedProvider{
		{Name: "my-openai", Endpoint: "api.openai.com"},
		{Name: "my-bedrock", Endpoint: "bedrock.us-east-1.amazonaws.com"},
	}
	hr := buildHTTPRoute("gpt4", "models", providers, 443,
		"default-gateway", "openshift-ingress", "300s", commonLabels("gpt4"))

	// 3 rules: path-based + 2 provider headers
	require.Len(t, hr.Spec.Rules, 3)

	// Rule 1: path-based (default backend = first provider)
	assert.Equal(t, "/models/gpt4", *hr.Spec.Rules[0].Matches[0].Path.Value)
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[0].BackendRefs[0].Name))

	// Rule 2: X-Selected-Provider: my-openai
	assert.Equal(t, "my-openai", hr.Spec.Rules[1].Matches[0].Headers[0].Value)
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[1].BackendRefs[0].Name))
	assert.Equal(t, "api.openai.com", hr.Spec.Rules[1].Filters[0].RequestHeaderModifier.Set[0].Value)

	// Rule 3: X-Selected-Provider: my-bedrock
	assert.Equal(t, "my-bedrock", hr.Spec.Rules[2].Matches[0].Headers[0].Value)
	assert.Equal(t, "my-bedrock", string(hr.Spec.Rules[2].BackendRefs[0].Name))
	assert.Equal(t, "bedrock.us-east-1.amazonaws.com", hr.Spec.Rules[2].Filters[0].RequestHeaderModifier.Set[0].Value)
}
