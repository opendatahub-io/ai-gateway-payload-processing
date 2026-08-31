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
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestExtractAndInjectMetadata(t *testing.T) {
	tests := []struct {
		name               string
		resp               *extProcPb.ProcessingResponse
		wantDynamicMeta    bool
		wantNamespace      string
		wantKey            string
		wantValues         []string
		wantRemainingHdrs  int
		wantPseudoStripped bool
	}{
		{
			name: "pseudo-header converted to DynamicMetadata",
			resp: makeRequestHeadersResponse(
				header("x-request-id", "abc123"),
				header(pseudoHeader, `{"ns":"envoy.lb.subset_hint","key":"x-gateway-destination-endpoint-subset","values":["spoke-east:443","spoke-west:443"]}`),
				header("content-type", "application/json"),
			),
			wantDynamicMeta:    true,
			wantNamespace:      "envoy.lb.subset_hint",
			wantKey:            "x-gateway-destination-endpoint-subset",
			wantValues:         []string{"spoke-east:443", "spoke-west:443"},
			wantRemainingHdrs:  2,
			wantPseudoStripped: true,
		},
		{
			name: "no pseudo-header leaves response unchanged",
			resp: makeRequestHeadersResponse(
				header("x-request-id", "abc123"),
				header("content-type", "application/json"),
			),
			wantDynamicMeta:   false,
			wantRemainingHdrs: 2,
		},
		{
			name: "single endpoint value",
			resp: makeRequestHeadersResponse(
				header(pseudoHeader, `{"ns":"envoy.lb.subset_hint","key":"x-gateway-destination-endpoint-subset","values":["spoke-east:443"]}`),
			),
			wantDynamicMeta:    true,
			wantNamespace:      "envoy.lb.subset_hint",
			wantKey:            "x-gateway-destination-endpoint-subset",
			wantValues:         []string{"spoke-east:443"},
			wantRemainingHdrs:  0,
			wantPseudoStripped: true,
		},
		{
			name: "ResponseHeaders pseudo-header stripped and metadata set",
			resp: makeResponseHeadersResponse(
				header(pseudoHeader, `{"ns":"envoy.lb.subset_hint","key":"x-gateway-destination-endpoint-subset","values":["spoke-east:443"]}`),
				header("x-custom", "keep"),
			),
			wantDynamicMeta:    true,
			wantNamespace:      "envoy.lb.subset_hint",
			wantKey:            "x-gateway-destination-endpoint-subset",
			wantValues:         []string{"spoke-east:443"},
			wantRemainingHdrs:  1,
			wantPseudoStripped: true,
		},
		{
			name: "RequestBody pseudo-header stripped and metadata set",
			resp: makeRequestBodyResponse(
				header(pseudoHeader, `{"ns":"envoy.lb.subset_hint","key":"x-gateway-destination-endpoint-subset","values":["spoke-west:443"]}`),
			),
			wantDynamicMeta:    true,
			wantNamespace:      "envoy.lb.subset_hint",
			wantKey:            "x-gateway-destination-endpoint-subset",
			wantValues:         []string{"spoke-west:443"},
			wantRemainingHdrs:  0,
			wantPseudoStripped: true,
		},
		{
			name: "ResponseBody pseudo-header stripped and metadata set",
			resp: makeResponseBodyResponse(
				header(pseudoHeader, `{"ns":"envoy.lb.subset_hint","key":"x-gateway-destination-endpoint-subset","values":["spoke-central:443"]}`),
			),
			wantDynamicMeta:    true,
			wantNamespace:      "envoy.lb.subset_hint",
			wantKey:            "x-gateway-destination-endpoint-subset",
			wantValues:         []string{"spoke-central:443"},
			wantRemainingHdrs:  0,
			wantPseudoStripped: true,
		},
		{
			name:            "ResponseBody without pseudo-header is a no-op",
			resp:            &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_ResponseBody{}},
			wantDynamicMeta: false,
		},
		{
			name:            "RequestBody without pseudo-header is a no-op",
			resp:            &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_RequestBody{}},
			wantDynamicMeta: false,
		},
		{
			name: "nil HeaderMutation is a no-op",
			resp: &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extProcPb.HeadersResponse{
						Response: &extProcPb.CommonResponse{},
					},
				},
			},
			wantDynamicMeta: false,
		},
		{
			name: "empty SetHeaders is a no-op",
			resp: &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extProcPb.HeadersResponse{
						Response: &extProcPb.CommonResponse{
							HeaderMutation: &extProcPb.HeaderMutation{
								SetHeaders: []*corev3.HeaderValueOption{},
							},
						},
					},
				},
			},
			wantDynamicMeta: false,
		},
		{
			name: "malformed JSON strips pseudo-header without setting metadata",
			resp: makeRequestHeadersResponse(
				header("x-request-id", "abc123"),
				header(pseudoHeader, `{invalid json}`),
			),
			wantDynamicMeta:   false,
			wantRemainingHdrs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractAndInjectMetadata(tt.resp)

			if !tt.wantDynamicMeta {
				assert.Nil(t, tt.resp.DynamicMetadata)
			} else {
				require.NotNil(t, tt.resp.DynamicMetadata)
				assertDynamicMetadata(t, tt.resp.DynamicMetadata, tt.wantNamespace, tt.wantKey, tt.wantValues)
			}

			if tt.wantPseudoStripped || tt.wantRemainingHdrs > 0 {
				cr := commonResponse(tt.resp)
				if cr != nil && cr.HeaderMutation != nil {
					hdrs := cr.HeaderMutation.SetHeaders
					if tt.wantPseudoStripped {
						assert.Len(t, hdrs, tt.wantRemainingHdrs)
						for _, h := range hdrs {
							assert.NotEqual(t, pseudoHeader, h.Header.Key, "pseudo-header must be stripped")
						}
					} else {
						assert.Len(t, hdrs, tt.wantRemainingHdrs)
					}
				}
			}
		})
	}
}

func TestWrapServer(t *testing.T) {
	inner := &extProcPb.UnimplementedExternalProcessorServer{}
	wrapped := WrapServer(inner)
	require.NotNil(t, wrapped)

	s, ok := wrapped.(*Server)
	require.True(t, ok, "WrapServer must return a *Server")
	assert.Equal(t, inner, s.inner, "inner server must be preserved")
}

// fakeProcessServer captures the response forwarded by wrappedStream.Send. The
// embedded (nil) interface satisfies the rest of ExternalProcessor_ProcessServer;
// only Send is exercised by wrappedStream.
type fakeProcessServer struct {
	extProcPb.ExternalProcessor_ProcessServer
	captured *extProcPb.ProcessingResponse
	sendErr  error
}

func (f *fakeProcessServer) Send(resp *extProcPb.ProcessingResponse) error {
	f.captured = resp
	return f.sendErr
}

func TestWrappedStreamSend(t *testing.T) {
	t.Run("strips pseudo-header, injects metadata, and forwards to inner", func(t *testing.T) {
		fake := &fakeProcessServer{}
		w := &wrappedStream{ExternalProcessor_ProcessServer: fake}

		resp := makeRequestHeadersResponse(
			header("x-request-id", "abc123"),
			header(pseudoHeader, `{"ns":"envoy.lb.subset_hint","key":"x-gateway-destination-endpoint-subset","values":["spoke-east:443","spoke-west:443"]}`),
		)

		require.NoError(t, w.Send(resp))

		require.NotNil(t, fake.captured, "response must be forwarded to the inner stream")

		hdrs := fake.captured.Response.(*extProcPb.ProcessingResponse_RequestHeaders).
			RequestHeaders.Response.HeaderMutation.SetHeaders
		require.Len(t, hdrs, 1, "pseudo-header must be stripped before forwarding")
		assert.Equal(t, "x-request-id", hdrs[0].Header.Key)

		require.NotNil(t, fake.captured.DynamicMetadata, "DynamicMetadata must be injected on the forwarded response")
		assertDynamicMetadata(t, fake.captured.DynamicMetadata,
			"envoy.lb.subset_hint", "x-gateway-destination-endpoint-subset",
			[]string{"spoke-east:443", "spoke-west:443"})
	})

	t.Run("forwards inner Send error unchanged", func(t *testing.T) {
		fake := &fakeProcessServer{sendErr: assert.AnError}
		w := &wrappedStream{ExternalProcessor_ProcessServer: fake}

		err := w.Send(makeRequestHeadersResponse(header("x-request-id", "abc123")))
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func makeRequestHeadersResponse(headers ...*corev3.HeaderValueOption) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extProcPb.HeadersResponse{
				Response: &extProcPb.CommonResponse{
					HeaderMutation: &extProcPb.HeaderMutation{
						SetHeaders: headers,
					},
				},
			},
		},
	}
}

func makeResponseHeadersResponse(headers ...*corev3.HeaderValueOption) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extProcPb.HeadersResponse{
				Response: &extProcPb.CommonResponse{
					HeaderMutation: &extProcPb.HeaderMutation{
						SetHeaders: headers,
					},
				},
			},
		},
	}
}

func makeRequestBodyResponse(headers ...*corev3.HeaderValueOption) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestBody{
			RequestBody: &extProcPb.BodyResponse{
				Response: &extProcPb.CommonResponse{
					HeaderMutation: &extProcPb.HeaderMutation{
						SetHeaders: headers,
					},
				},
			},
		},
	}
}

func makeResponseBodyResponse(headers ...*corev3.HeaderValueOption) *extProcPb.ProcessingResponse {
	return &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_ResponseBody{
			ResponseBody: &extProcPb.BodyResponse{
				Response: &extProcPb.CommonResponse{
					HeaderMutation: &extProcPb.HeaderMutation{
						SetHeaders: headers,
					},
				},
			},
		},
	}
}

func header(key, value string) *corev3.HeaderValueOption {
	return &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      key,
			RawValue: []byte(value),
		},
	}
}

func assertDynamicMetadata(t *testing.T, meta *structpb.Struct, namespace, key string, wantValues []string) {
	t.Helper()

	nsField, ok := meta.Fields[namespace]
	require.True(t, ok, "namespace %q not found in DynamicMetadata", namespace)

	nsStruct := nsField.GetStructValue()
	require.NotNil(t, nsStruct, "namespace %q value is not a struct", namespace)

	keyField, ok := nsStruct.Fields[key]
	require.True(t, ok, "key %q not found in namespace %q", key, namespace)

	listVal := keyField.GetListValue()
	require.NotNil(t, listVal, "key %q value is not a list", key)
	require.Len(t, listVal.Values, len(wantValues))

	for i, want := range wantValues {
		assert.Equal(t, want, listVal.Values[i].GetStringValue(), "value at index %d", i)
	}
}
