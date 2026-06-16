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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

func TestSimpleGenerateAuthHeaders(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]string
		wantHeaders map[string]string
		wantErr     bool
	}{
		{
			name: "Bearer prefix (OpenAI style)",
			credentials: map[string]string{
				"api-key":                  "sk-test-key",
				auth.SimpleAuthHeaderName:  "Authorization",
				auth.SimpleAuthValuePrefix: "Bearer ",
			},
			wantHeaders: map[string]string{
				"Authorization": "Bearer sk-test-key",
			},
		},
		{
			name: "raw key without prefix (Anthropic style)",
			credentials: map[string]string{
				"api-key":                  "ant-key-123",
				auth.SimpleAuthHeaderName:  "x-api-key",
				auth.SimpleAuthValuePrefix: "",
			},
			wantHeaders: map[string]string{
				"x-api-key": "ant-key-123",
			},
		},
		{
			name: "missing api-key field returns error",
			credentials: map[string]string{
				"wrong-field":              "some-value",
				auth.SimpleAuthHeaderName:  "Authorization",
				auth.SimpleAuthValuePrefix: "Bearer ",
			},
			wantErr: true,
		},
		{
			name:        "empty credentials returns error",
			credentials: map[string]string{},
			wantErr:     true,
		},
		{
			name: "missing authHeaderName returns error",
			credentials: map[string]string{
				"api-key":                  "sk-test-key",
				auth.SimpleAuthValuePrefix: "Bearer ",
			},
			wantErr: true,
		},
		{
			name: "missing authValuePrefix returns error",
			credentials: map[string]string{
				"api-key":                 "sk-test-key",
				auth.SimpleAuthHeaderName: "Authorization",
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := NewSimpleAuthGenerator()
			authHeaders, err := generator.GenerateAuthHeaders(test.credentials)

			if test.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if diff := cmp.Diff(test.wantHeaders, authHeaders, cmpopts.SortMaps(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSimpleExtractRequestData(t *testing.T) {
	tests := []struct {
		name            string
		config          map[string]string
		wantHeaderName  string
		wantValuePrefix string
	}{
		{
			name:            "no config defaults to Authorization Bearer",
			config:          map[string]string{},
			wantHeaderName:  "Authorization",
			wantValuePrefix: "Bearer ",
		},
		{
			name:            "config with custom header name clears value prefix",
			config:          map[string]string{auth.SimpleAuthHeaderName: "x-api-key"},
			wantHeaderName:  "x-api-key",
			wantValuePrefix: "",
		},
		{
			name:            "config with header name and explicit prefix",
			config:          map[string]string{auth.SimpleAuthHeaderName: "x-api-key", auth.SimpleAuthValuePrefix: "Key "},
			wantHeaderName:  "x-api-key",
			wantValuePrefix: "Key ",
		},
		{
			name:            "config with empty header name defaults to Authorization Bearer",
			config:          map[string]string{auth.SimpleAuthHeaderName: ""},
			wantHeaderName:  "Authorization",
			wantValuePrefix: "Bearer ",
		},
		{
			name:            "config with value prefix only is ignored without header name",
			config:          map[string]string{auth.SimpleAuthValuePrefix: "Token "},
			wantHeaderName:  "Authorization",
			wantValuePrefix: "Bearer ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cs := framework.NewCycleState()
			cs.Write(state.ModelConfigKey, test.config)

			generator := NewSimpleAuthGenerator()
			result, err := generator.ExtractRequestData(cs, framework.NewInferenceRequest())

			require.NoError(t, err)
			require.Equal(t, test.wantHeaderName, result[auth.SimpleAuthHeaderName])
			require.Equal(t, test.wantValuePrefix, result[auth.SimpleAuthValuePrefix])
		})
	}
}

func TestSimpleExtractRequestData_MissingModelConfigKey(t *testing.T) {
	generator := NewSimpleAuthGenerator()
	_, err := generator.ExtractRequestData(framework.NewCycleState(), framework.NewInferenceRequest())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to extract config from cycle state")
}
