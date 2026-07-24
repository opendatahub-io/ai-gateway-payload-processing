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

package maas_headers_guard

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

func TestProcessRequest_StripsInternalHeaders(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers["x-maas-username"] = "alice"
	req.Headers["x-maas-group"] = "engineering"
	req.Headers["x-maas-subscription"] = "premium"
	req.Headers["content-type"] = "application/json"
	req.Headers["authorization"] = "Bearer sk-oai-test"

	err := instance.(*Plugin).ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	// Internal headers stripped
	_, hasUser := req.Headers["x-maas-username"]
	assert.False(t, hasUser, "x-maas-username must be stripped")
	_, hasGroup := req.Headers["x-maas-group"]
	assert.False(t, hasGroup, "x-maas-group must be stripped")
	_, hasSub := req.Headers["x-maas-subscription"]
	assert.False(t, hasSub, "x-maas-subscription must be stripped")

	// Auth headers stripped (must not leak to upstream model)
	_, hasAuth := req.Headers["authorization"]
	assert.False(t, hasAuth, "authorization must be stripped")

	// Non-internal headers preserved
	assert.Equal(t, "application/json", req.Headers["content-type"])

	// All values saved in CycleState as a single map
	captured, err := plugin.ReadCycleStateKey[map[string]string](cs, MaaSHeadersKey)
	require.NoError(t, err)
	assert.Equal(t, "alice", captured["x-maas-username"])
	assert.Equal(t, "engineering", captured["x-maas-group"])
	assert.Equal(t, "premium", captured["x-maas-subscription"])
}

func TestProcessRequest_StripsXApiKey(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers["x-api-key"] = "sk-oai-test"
	req.Headers["content-type"] = "application/json"

	err := instance.(*Plugin).ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, hasKey := req.Headers["x-api-key"]
	assert.False(t, hasKey, "x-api-key must be stripped")
	assert.Equal(t, "application/json", req.Headers["content-type"])
}

func TestProcessRequest_NoInternalHeaders(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers["content-type"] = "application/json"

	err := instance.(*Plugin).ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	assert.Equal(t, "application/json", req.Headers["content-type"])

	// Empty map in CycleState (always written, even when no maas headers found)
	captured, err := plugin.ReadCycleStateKey[map[string]string](cs, MaaSHeadersKey)
	require.NoError(t, err)
	assert.Empty(t, captured)
}

func TestProcessRequest_CaseInsensitive(t *testing.T) {
	instance, _ := Factory("test", nil, nil)
	cs := plugin.NewCycleState()
	req := requesthandling.NewInferenceRequest()
	req.Headers["X-MaaS-Username"] = "bob"

	err := instance.(*Plugin).ProcessRequest(context.Background(), cs, req)
	require.NoError(t, err)

	_, hasHeader := req.Headers["X-MaaS-Username"]
	assert.False(t, hasHeader, "mixed-case x-maas header must be stripped")

	captured, err := plugin.ReadCycleStateKey[map[string]string](cs, MaaSHeadersKey)
	require.NoError(t, err)
	assert.Equal(t, "bob", captured["X-MaaS-Username"])
}
