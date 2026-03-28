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

package apikey_generation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIKeyGenerator(t *testing.T) {
	tests := []struct {
		name            string
		headerName      string
		headerPrefix    string
		apiKey          string
		wantHeaderName  string
		wantHeaderValue string
	}{
		{
			name:            "Bearer prefix (OpenAI style)",
			headerName:      "Authorization",
			headerPrefix:    "Bearer ",
			apiKey:          "sk-test-key",
			wantHeaderName:  "Authorization",
			wantHeaderValue: "Bearer sk-test-key",
		},
		{
			name:            "raw key without prefix (Anthropic style)",
			headerName:      "x-api-key",
			apiKey:          "ant-key-123",
			wantHeaderName:  "x-api-key",
			wantHeaderValue: "ant-key-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := &SimpleApiKeyGenerator{
				HeaderName:        tt.headerName,
				HeaderValuePrefix: tt.headerPrefix,
			}

			gotName, gotValue := generator.GenerateHeader(tt.apiKey)

			assert.Equal(t, tt.wantHeaderName, gotName)
			assert.Equal(t, tt.wantHeaderValue, gotValue)
		})
	}
}
