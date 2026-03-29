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

package auth

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestSimpleAuthHeadersGenerator(t *testing.T) {
	tests := []struct {
		name         string
		headerName   string
		headerPrefix string
		apiKey       string
		wantHeaders  map[string]string
	}{
		{
			name:         "Bearer prefix (OpenAI style)",
			headerName:   "Authorization",
			headerPrefix: "Bearer ",
			apiKey:       "sk-test-key",
			wantHeaders: map[string]string{
				"Authorization": "Bearer sk-test-key",
			},
		},
		{
			name:       "raw key without prefix (Anthropic style)",
			headerName: "x-api-key",
			apiKey:     "ant-key-123",
			wantHeaders: map[string]string{
				"x-api-key": "ant-key-123",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := &SimpleAuthGenerator{HeaderName: test.headerName, HeaderValuePrefix: test.headerPrefix}
			authHeaders := generator.GenerateAuthHeaders(test.apiKey)
			if diff := cmp.Diff(test.wantHeaders, authHeaders, cmpopts.SortMaps(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
