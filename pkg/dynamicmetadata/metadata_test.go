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

package dynamicmetadata

import (
	"encoding/json"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetEndpointSubset(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []string
	}{
		{
			name:      "multiple endpoints",
			endpoints: []string{"spoke-east:443", "spoke-west:443", "spoke-central:443"},
		},
		{
			name:      "single endpoint",
			endpoints: []string{"spoke-east:443"},
		},
		{
			name:      "empty list",
			endpoints: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := requesthandling.NewInferenceRequest()
			err := SetEndpointSubset(req, tt.endpoints)
			require.NoError(t, err)

			raw, ok := req.MutatedHeaders()[pseudoHeader]
			require.True(t, ok, "pseudo-header must be set in mutated headers")

			var e entry
			err = json.Unmarshal([]byte(raw), &e)
			require.NoError(t, err, "pseudo-header value must be valid JSON")

			assert.Equal(t, "envoy.lb.subset_hint", e.Namespace)
			assert.Equal(t, "x-gateway-destination-endpoint-subset", e.Key)
			assert.Equal(t, tt.endpoints, e.Values)
		})
	}
}
