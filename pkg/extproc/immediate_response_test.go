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

package extproc

import (
	"encoding/json"
	"strconv"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompleteImmediateResponse(t *testing.T) {
	const refusal = "[MAAS-GUARDRAILS-BLOCKED] Your request was blocked by Westpac's MaaS Guardrails policy."

	t.Run("plain inference error becomes a JSON body with length", func(t *testing.T) {
		resp := &extProcPb.ProcessingResponse{
			Response: &extProcPb.ProcessingResponse_ImmediateResponse{
				ImmediateResponse: &extProcPb.ImmediateResponse{
					Status: &envoyTypePb.HttpStatus{Code: envoyTypePb.StatusCode_Forbidden},
					Body:   []byte("inference error: Forbidden - " + refusal),
				},
			},
		}

		completeImmediateResponse(resp)

		ir := resp.GetImmediateResponse()
		require.NotEmpty(t, ir.GetBody())
		assert.Equal(t, strconv.Itoa(len(ir.GetBody())), headerValue(t, ir, headerContentLength))
		assert.Equal(t, contentTypeJSON, headerValue(t, ir, headerContentType))
		assert.Equal(t, "forbidden", ir.GetDetails())

		var parsed apiError
		require.NoError(t, json.Unmarshal(ir.GetBody(), &parsed))
		assert.Equal(t, refusal, parsed.Error.Message)
		assert.Equal(t, "invalid_request_error", parsed.Error.Type)
		assert.Equal(t, "content_filter", parsed.Error.Code)
	})

	t.Run("empty body still produces a displayable JSON payload", func(t *testing.T) {
		resp := &extProcPb.ProcessingResponse{
			Response: &extProcPb.ProcessingResponse_ImmediateResponse{
				ImmediateResponse: &extProcPb.ImmediateResponse{
					Status: &envoyTypePb.HttpStatus{Code: envoyTypePb.StatusCode_Forbidden},
				},
			},
		}

		completeImmediateResponse(resp)

		ir := resp.GetImmediateResponse()
		require.NotEmpty(t, ir.GetBody())
		assert.Equal(t, strconv.Itoa(len(ir.GetBody())), headerValue(t, ir, headerContentLength))

		var parsed apiError
		require.NoError(t, json.Unmarshal(ir.GetBody(), &parsed))
		assert.NotEmpty(t, parsed.Error.Message)
	})

	t.Run("existing JSON object is kept and length is corrected", func(t *testing.T) {
		raw := []byte(`{"error":{"message":"already json","type":"invalid_request_error","code":"content_filter"}}`)
		resp := &extProcPb.ProcessingResponse{
			Response: &extProcPb.ProcessingResponse_ImmediateResponse{
				ImmediateResponse: &extProcPb.ImmediateResponse{
					Status: &envoyTypePb.HttpStatus{Code: envoyTypePb.StatusCode_Forbidden},
					Body:   raw,
					Headers: &extProcPb.HeaderMutation{
						SetHeaders: []*corev3.HeaderValueOption{{
							Header: &corev3.HeaderValue{Key: "content-length", Value: "0"},
						}},
					},
				},
			},
		}

		completeImmediateResponse(resp)

		ir := resp.GetImmediateResponse()
		assert.JSONEq(t, string(raw), string(ir.GetBody()))
		assert.Equal(t, strconv.Itoa(len(ir.GetBody())), headerValue(t, ir, headerContentLength))
		assert.Equal(t, contentTypeJSON, headerValue(t, ir, headerContentType))
		assert.Len(t, ir.GetHeaders().GetSetHeaders(), 2)
	})

	t.Run("non-immediate response is left alone", func(t *testing.T) {
		resp := &extProcPb.ProcessingResponse{
			Response: &extProcPb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extProcPb.HeadersResponse{},
			},
		}
		completeImmediateResponse(resp)
		assert.NotNil(t, resp.GetRequestHeaders())
		assert.Nil(t, resp.GetImmediateResponse())
	})
}

func headerValue(t *testing.T, ir *extProcPb.ImmediateResponse, key string) string {
	t.Helper()
	for _, h := range ir.GetHeaders().GetSetHeaders() {
		if h.GetHeader() == nil || h.Header.Key != key {
			continue
		}
		if len(h.Header.RawValue) > 0 {
			return string(h.Header.RawValue)
		}
		return h.Header.Value
	}
	t.Fatalf("header %s not set", key)
	return ""
}
