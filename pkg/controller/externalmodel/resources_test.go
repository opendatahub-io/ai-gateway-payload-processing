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

func TestBuildHTTPRoute(t *testing.T) {
	hr := buildHTTPRoute(
		"api.openai.com", "my-openai",
		"gpt4", "gpt-4o",
		"models", 443,
		"default-gateway", "openshift-ingress", "300s",
		commonLabels("gpt4"),
	)

	assert.Equal(t, "gpt4", hr.Name)
	assert.Equal(t, "models", hr.Namespace)
	assert.Equal(t, managedByValue, hr.Labels[labelManagedBy])

	// Parent gateway ref
	require.Len(t, hr.Spec.ParentRefs, 1)
	assert.Equal(t, "default-gateway", string(hr.Spec.ParentRefs[0].Name))
	assert.Equal(t, "openshift-ingress", string(*hr.Spec.ParentRefs[0].Namespace))

	// Must have 2 rules: path-based and header-based
	require.Len(t, hr.Spec.Rules, 2)

	// Rule 1: path-based match with namespace prefix
	rule1 := hr.Spec.Rules[0]
	assert.Equal(t, "/models/gpt4", *rule1.Matches[0].Path.Value)

	// Rule 2: header-based match uses targetModel
	rule2 := hr.Spec.Rules[1]
	assert.Equal(t, "X-Gateway-Model-Name", string(rule2.Matches[0].Headers[0].Name))
	assert.Equal(t, "gpt-4o", rule2.Matches[0].Headers[0].Value)

	// Backend ref points to the PROVIDER's Service, not the model
	for i, rule := range hr.Spec.Rules {
		require.Len(t, rule.BackendRefs, 1, "rule %d", i)
		assert.Equal(t, "my-openai", string(rule.BackendRefs[0].Name),
			"rule %d: backend should be the provider's Service", i)
	}

	// Host header filter for TLS SNI uses provider endpoint
	for i, rule := range hr.Spec.Rules {
		require.Len(t, rule.Filters, 1, "rule %d", i)
		assert.Equal(t, gatewayapiv1.HTTPRouteFilterRequestHeaderModifier, rule.Filters[0].Type)
		assert.Equal(t, "Host", string(rule.Filters[0].RequestHeaderModifier.Set[0].Name))
		assert.Equal(t, "api.openai.com", rule.Filters[0].RequestHeaderModifier.Set[0].Value)
	}
}

func TestBuildHTTPRoute_TargetModelDiffersFromName(t *testing.T) {
	hr := buildHTTPRoute(
		"bedrock.us-east-1.amazonaws.com", "my-bedrock",
		"claude", "anthropic.claude-3-opus",
		"models", 443,
		"my-gateway", "gateway-ns", "300s",
		commonLabels("claude"),
	)

	// Name and path use ExternalModel name
	assert.Equal(t, "claude", hr.Name)
	assert.Equal(t, "/models/claude", *hr.Spec.Rules[0].Matches[0].Path.Value)

	// Header match uses targetModel (provider-side name)
	assert.Equal(t, "anthropic.claude-3-opus", hr.Spec.Rules[1].Matches[0].Headers[0].Value)

	// Backend points to provider Service
	assert.Equal(t, "my-bedrock", string(hr.Spec.Rules[0].BackendRefs[0].Name))

	// Host header uses provider endpoint
	assert.Equal(t, "bedrock.us-east-1.amazonaws.com",
		hr.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)
}
