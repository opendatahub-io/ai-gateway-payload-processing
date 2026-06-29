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

package stream_usage_enforcer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
)

func TestFactory(t *testing.T) {
	p, err := Factory("test", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "test", p.TypedName().Name)
	assert.Equal(t, PluginType, p.TypedName().Type)
}

func TestProcessRequest_NonStreamingRequest(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "gpt-oss-20b"
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hi"}}

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)
	assert.Nil(t, req.Body["stream_options"], "should not add stream_options to non-streaming request")
	assert.False(t, req.BodyMutated())
}

func TestProcessRequest_StreamFalse(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["stream"] = false

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)
	assert.Nil(t, req.Body["stream_options"])
	assert.False(t, req.BodyMutated())
}

func TestProcessRequest_StreamTrueNoStreamOptions(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "gpt-oss-20b"
	req.Body["stream"] = true
	req.Body["messages"] = []any{map[string]any{"role": "user", "content": "Hello"}}

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)

	opts, ok := req.Body["stream_options"].(map[string]any)
	require.True(t, ok, "stream_options should be set")
	assert.Equal(t, true, opts["include_usage"])
	assert.True(t, req.BodyMutated())
}

func TestProcessRequest_StreamTrueIncludeUsageFalse(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["stream"] = true
	req.Body["stream_options"] = map[string]any{"include_usage": false}

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)

	opts := req.Body["stream_options"].(map[string]any)
	assert.Equal(t, true, opts["include_usage"], "should override false to true")
	assert.True(t, req.BodyMutated())
}

func TestProcessRequest_StreamTrueIncludeUsageAlreadyTrue(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["stream"] = true
	req.Body["stream_options"] = map[string]any{"include_usage": true}

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)

	opts := req.Body["stream_options"].(map[string]any)
	assert.Equal(t, true, opts["include_usage"])
	assert.False(t, req.BodyMutated(), "should not mutate when already correct")
}

func TestProcessRequest_PreservesExistingStreamOptions(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["stream"] = true
	req.Body["stream_options"] = map[string]any{
		"continuous_usage_stats": true,
	}

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)

	opts := req.Body["stream_options"].(map[string]any)
	assert.Equal(t, true, opts["include_usage"], "include_usage should be added")
	assert.Equal(t, true, opts["continuous_usage_stats"], "existing fields should be preserved")
	assert.True(t, req.BodyMutated())
}

func TestProcessRequest_StreamFieldAbsent(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	req := requesthandling.NewInferenceRequest()
	req.Body["model"] = "gpt-oss-20b"

	err := instance.(*Plugin).ProcessRequest(context.Background(), plugin.NewCycleState(), req)
	require.NoError(t, err)
	assert.Nil(t, req.Body["stream_options"])
	assert.False(t, req.BodyMutated())
}
