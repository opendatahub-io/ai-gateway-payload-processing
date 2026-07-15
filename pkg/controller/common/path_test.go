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
	"github.com/stretchr/testify/require"
)

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		config      map[string]string
		targetModel string
		want        string
		wantErr     bool
	}{
		{"empty path", "", nil, "", "", false},
		{"no placeholders", "/v1/chat/completions", map[string]string{"k": "v"}, "", "/v1/chat/completions", false},
		{"single placeholder resolved", "/v1/{project}/chat", map[string]string{"project": "my-proj"}, "", "/v1/my-proj/chat", false},
		{"multiple placeholders resolved", "/{location}/{project}/models", map[string]string{"location": "us", "project": "p1"}, "", "/us/p1/models", false},
		{"unresolved placeholder returns error", "/v1/{unknown}/chat", map[string]string{"other": "val"}, "", "", true},
		{"nil config with placeholder returns error", "/v1/{key}/x", nil, "", "", true},
		{"nil config no placeholder", "/v1/chat", nil, "", "/v1/chat", false},
		{"partial resolution returns error", "/v1/{project}/{location}/chat", map[string]string{"project": "p1"}, "", "", true},
		{
			"model placeholder resolved from targetModel",
			"/v1/publishers/anthropic/models/{model}:rawPredict",
			nil,
			"claude-sonnet-4-20250514",
			"/v1/publishers/anthropic/models/claude-sonnet-4-20250514:rawPredict",
			false,
		},
		{
			"model placeholder with config and targetModel",
			"/v1/{project}/models/{model}:predict",
			map[string]string{"project": "my-proj"},
			"gemini-pro",
			"/v1/my-proj/models/gemini-pro:predict",
			false,
		},
		{
			"explicit model in config overrides targetModel",
			"/v1/models/{model}/chat",
			map[string]string{"model": "custom-model"},
			"target-model",
			"/v1/models/custom-model/chat",
			false,
		},
		{
			"model placeholder without targetModel resolves to empty",
			"/v1/models/{model}/chat",
			nil,
			"",
			"/v1/models//chat",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePath(tt.path, tt.config, tt.targetModel)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unresolved placeholders")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
