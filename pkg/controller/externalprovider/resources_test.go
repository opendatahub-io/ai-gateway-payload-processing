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

package externalprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestCommonLabels(t *testing.T) {
	labels := commonLabels("my-openai")

	assert.Equal(t, managedByValue, labels[labelManagedBy])
	assert.Equal(t, "my-openai", labels[labelExternalProvider])
	assert.Len(t, labels, 2)
}

func TestBuildService(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		svcName   string
		namespace string
		port      int32
	}{
		{
			name:      "standard OpenAI provider",
			endpoint:  "api.openai.com",
			svcName:   "my-openai",
			namespace: "models",
			port:      443,
		},
		{
			name:      "custom port provider",
			endpoint:  "bedrock.us-east-1.amazonaws.com",
			svcName:   "my-bedrock",
			namespace: "llm",
			port:      8443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := buildService(tt.endpoint, tt.svcName, tt.namespace, tt.port, commonLabels(tt.svcName))

			assert.Equal(t, tt.svcName, svc.Name)
			assert.Equal(t, tt.namespace, svc.Namespace)
			assert.Equal(t, corev1.ServiceTypeExternalName, svc.Spec.Type)
			assert.Equal(t, tt.endpoint, svc.Spec.ExternalName)
			require.Len(t, svc.Spec.Ports, 1)
			assert.Equal(t, tt.port, svc.Spec.Ports[0].Port)
			assert.Equal(t, tt.port, svc.Spec.Ports[0].TargetPort.IntVal)
			assert.Equal(t, managedByValue, svc.Labels[labelManagedBy])
			assert.Equal(t, tt.svcName, svc.Labels[labelExternalProvider])
		})
	}
}

func TestBuildServiceEntry(t *testing.T) {
	se := buildServiceEntry("api.openai.com", "my-openai", "models", 443, commonLabels("my-openai"))

	assert.Equal(t, "ServiceEntry", se.GetKind())
	assert.Equal(t, "networking.istio.io/v1", se.GetAPIVersion())
	assert.Equal(t, "my-openai", se.GetName())
	assert.Equal(t, "models", se.GetNamespace())
	assert.Equal(t, managedByValue, se.GetLabels()[labelManagedBy])

	spec, ok := se.Object["spec"].(map[string]any)
	require.True(t, ok)

	hosts, ok := spec["hosts"].([]any)
	require.True(t, ok)
	assert.Equal(t, "api.openai.com", hosts[0])

	assert.Equal(t, "MESH_EXTERNAL", spec["location"])
	assert.Equal(t, "DNS", spec["resolution"])

	ports, ok := spec["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)
	port, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(443), port["number"])
	assert.Equal(t, "https", port["name"])
	assert.Equal(t, "HTTPS", port["protocol"])
}

func TestBuildServiceEntry_CustomPort(t *testing.T) {
	se := buildServiceEntry("vllm.internal.svc", "my-vllm", "models", 8443, commonLabels("my-vllm"))

	spec, ok := se.Object["spec"].(map[string]any)
	require.True(t, ok)
	ports, ok := spec["ports"].([]any)
	require.True(t, ok)
	port, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(8443), port["number"])
}

func TestBuildDestinationRule(t *testing.T) {
	dr := buildDestinationRule("api.openai.com", "my-openai", "models", commonLabels("my-openai"))

	assert.Equal(t, "DestinationRule", dr.GetKind())
	assert.Equal(t, "networking.istio.io/v1", dr.GetAPIVersion())
	assert.Equal(t, "my-openai", dr.GetName())
	assert.Equal(t, "models", dr.GetNamespace())
	assert.Equal(t, managedByValue, dr.GetLabels()[labelManagedBy])

	spec, ok := dr.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "api.openai.com", spec["host"])

	trafficPolicy, ok := spec["trafficPolicy"].(map[string]any)
	require.True(t, ok)
	tls, ok := trafficPolicy["tls"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "SIMPLE", tls["mode"])
}

func TestBuildDestinationRule_DifferentProviders(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		provider string
	}{
		{"anthropic", "api.anthropic.com", "my-anthropic"},
		{"bedrock", "bedrock.us-east-1.amazonaws.com", "my-bedrock"},
		{"azure", "my-deployment.openai.azure.com", "my-azure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dr := buildDestinationRule(tt.endpoint, tt.provider, "models", commonLabels(tt.provider))

			spec, ok := dr.Object["spec"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.endpoint, spec["host"])
			assert.Equal(t, tt.provider, dr.GetName())
		})
	}
}
