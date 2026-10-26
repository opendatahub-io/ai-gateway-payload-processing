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

package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

func testProviderRef(namespace string) inferencev1alpha1.ExternalProviderRef {
	return inferencev1alpha1.ExternalProviderRef{
		Ref:         inferencev1alpha1.ExternalProviderReference{Name: "provider", Namespace: namespace},
		TargetModel: "gpt-4o",
		APIFormat:   "openai-chat",
		Path:        "/v1/chat/completions",
	}
}

func testModel(refs ...inferencev1alpha1.ExternalProviderRef) *inferencev1alpha1.ExternalModel {
	return &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "models"},
		Spec:       inferencev1alpha1.ExternalModelSpec{ExternalProviderRefs: refs},
	}
}

func withGatewayRefs(m *inferencev1alpha1.ExternalModel) *inferencev1alpha1.ExternalModel {
	m.Spec.GatewayRefs = []inferencev1alpha1.NamespacedObjectReference{
		{Name: "gateway", Namespace: "gateway-ns"},
	}
	return m
}

func TestIsAGCManaged(t *testing.T) {
	providerRef := testProviderRef
	model := testModel

	tests := []struct {
		name  string
		model *inferencev1alpha1.ExternalModel
		want  bool
	}{
		{
			name:  "legacy shape with omitted provider namespace",
			model: model(providerRef("")),
			want:  false,
		},
		{
			name:  "legacy shape with explicit same-namespace provider reference",
			model: model(providerRef("models")),
			want:  false,
		},
		{
			name:  "AGC shape with gatewayRefs",
			model: withGatewayRefs(model(providerRef(""))),
			want:  true,
		},
		{
			name:  "AGC shape with gatewayRefs alongside local references",
			model: withGatewayRefs(model(providerRef(""), providerRef("models"))),
			want:  true,
		},
		{
			name:  "AGC shape with cross-namespace provider reference",
			model: model(providerRef("shared")),
			want:  true,
		},
		{
			name:  "AGC shape with only cross-namespace provider references",
			model: model(providerRef("shared"), providerRef("other")),
			want:  true,
		},
		{
			name: "mixed omitted-local and cross-namespace references keep IPP serving",
			model: model(
				providerRef(""),
				providerRef("shared"),
			),
			want: false,
		},
		{
			name: "mixed explicit-local and cross-namespace references keep IPP serving",
			model: model(
				providerRef("models"),
				providerRef("shared"),
			),
			want: false,
		},
		{
			name:  "no references at all is not AGC-managed",
			model: model(),
			want:  false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsAGCManaged(tc.model))
		})
	}
}

func TestIsAGCAttached(t *testing.T) {
	tests := []struct {
		name  string
		model *inferencev1alpha1.ExternalModel
		want  bool
	}{
		{
			name:  "gatewayRefs attachment",
			model: withGatewayRefs(testModel(testProviderRef(""))),
			want:  true,
		},
		{
			name:  "gatewayRefs attachment alongside local references",
			model: withGatewayRefs(testModel(testProviderRef(""), testProviderRef("models"))),
			want:  true,
		},
		{
			name:  "gatewayRefs attachment alongside foreign-only references",
			model: withGatewayRefs(testModel(testProviderRef("shared"))),
			want:  true,
		},
		{
			name:  "local references without gatewayRefs",
			model: testModel(testProviderRef("")),
			want:  false,
		},
		{
			name:  "foreign-only references without gatewayRefs are IPP-reported, not skipped",
			model: testModel(testProviderRef("shared")),
			want:  false,
		},
		{
			name:  "no references and no gatewayRefs",
			model: testModel(),
			want:  false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsAGCAttached(tc.model))
		})
	}
}
