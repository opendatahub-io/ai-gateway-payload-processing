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

package passthrough_profile_picker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/state"
)

func testProfiles() map[string]*requesthandling.Profile {
	return map[string]*requesthandling.Profile{
		"translation":  {},
		"passthrough":  {},
	}
}

func TestPick(t *testing.T) {
	tests := []struct {
		name         string
		inputFormat  apiformat.APIFormat
		outputFormat apiformat.APIFormat
		wantProfile  string
	}{
		{
			name:        "no format keys (resolver did not run) — fail safe to translation",
			wantProfile: "translation",
		},
		{
			name:         "input only, no output (internal model)",
			inputFormat:  apiformat.OpenAIChatCompletions,
			wantProfile:  "passthrough",
		},
		{
			name:         "input only, non-openai (internal model)",
			inputFormat:  apiformat.Messages,
			wantProfile:  "passthrough",
		},
		{
			name:         "messages to messages (passthrough)",
			inputFormat:  apiformat.Messages,
			outputFormat: apiformat.Messages,
			wantProfile:  "passthrough",
		},
		{
			name:         "openai to anthropic (translation needed)",
			inputFormat:  apiformat.OpenAIChatCompletions,
			outputFormat: apiformat.Messages,
			wantProfile:  "translation",
		},
		{
			name:         "openai to openai (translation - path rewriting)",
			inputFormat:  apiformat.OpenAIChatCompletions,
			outputFormat: apiformat.OpenAIChatCompletions,
			wantProfile:  "translation",
		},
		{
			name:         "openai to vertex-messages (translation needed)",
			inputFormat:  apiformat.OpenAIChatCompletions,
			outputFormat: apiformat.VertexMessages,
			wantProfile:  "translation",
		},
		{
			name:         "openai-responses to openai-responses (passthrough)",
			inputFormat:  apiformat.OpenAIResponses,
			outputFormat: apiformat.OpenAIResponses,
			wantProfile:  "passthrough",
		},
	}

	picker, err := Factory(PassthroughProfilePickerType, nil, nil)
	require.NoError(t, err)
	pp := picker.(*PassthroughProfilePicker)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cs := plugin.NewCycleState()
			if tc.inputFormat != "" {
				cs.Write(state.InputAPIFormatKey, tc.inputFormat)
			}
			if tc.outputFormat != "" {
				cs.Write(state.APIFormatKey, tc.outputFormat)
			}

			profile, err := pp.Pick(context.Background(), cs, nil, testProfiles())
			require.NoError(t, err)
			assert.Equal(t, testProfiles()[tc.wantProfile], profile)
		})
	}
}

func TestPick_MissingProfile(t *testing.T) {
	picker, err := Factory(PassthroughProfilePickerType, nil, nil)
	require.NoError(t, err)

	profiles := map[string]*requesthandling.Profile{
		"translation": {},
	}

	cs := plugin.NewCycleState()
	cs.Write(state.InputAPIFormatKey, apiformat.Messages)
	cs.Write(state.APIFormatKey, apiformat.Messages)
	_, err = picker.(*PassthroughProfilePicker).Pick(context.Background(), cs, nil, profiles)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "passthrough")
}

func TestFactory_CustomProfileNames(t *testing.T) {
	params := []byte(`{"translationProfile":"custom-translate","passthroughProfile":"custom-pass"}`)
	p, err := Factory("test", params, nil)
	require.NoError(t, err)

	pp := p.(*PassthroughProfilePicker)
	assert.Equal(t, "custom-translate", pp.translationProfile)
	assert.Equal(t, "custom-pass", pp.passthroughProfile)
}

func TestFactory_Defaults(t *testing.T) {
	p, err := Factory("test", nil, nil)
	require.NoError(t, err)

	pp := p.(*PassthroughProfilePicker)
	assert.Equal(t, "translation", pp.translationProfile)
	assert.Equal(t, "passthrough", pp.passthroughProfile)
}
