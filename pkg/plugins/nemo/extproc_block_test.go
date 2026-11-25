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

package nemo

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/requesthandling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/handlers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/dynamicmetadata"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/extproc"
)

const blockedRefusal = "[MAAS-GUARDRAILS-BLOCKED] Your request was blocked by Westpac's MaaS Guardrails policy."

// TestBlockedInputClosesExtProcStream drives the same ext_proc path the gateway
// uses. A blocked NeMo input check must come back as one ImmediateResponse
// with a non-empty JSON body and then end the stream, for both a single body
// chunk and a body split the way a streaming client delivers it.
func TestBlockedInputClosesExtProcStream(t *testing.T) {
	refusal := blockedRefusal
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "blocked",
			"content": refusal,
		})
	}))
	t.Cleanup(srv.Close)

	plugin, err := NewNemoRequestGuardPlugin(srv.URL, 5)
	require.NoError(t, err)
	trustTestServer(t, plugin.httpClient, srv)

	extProcServer := extproc.WrapServer(dynamicmetadata.WrapServer(handlers.NewServer(
		[]requesthandling.RequestProcessor{plugin},
		nil,
		nil,
		nil,
		nil,
	)))

	tests := []struct {
		name   string
		path   string
		body   func(t *testing.T) []byte
		chunks func(body []byte) []bodyChunk
	}{
		{
			name: "non-streaming chat completion",
			path: "/v1/chat/completions",
			body: func(t *testing.T) []byte { return chatBody(t, false) },
			chunks: func(body []byte) []bodyChunk {
				return []bodyChunk{{data: body, end: true}}
			},
		},
		{
			name: "streaming chat completion",
			path: "/v1/chat/completions",
			body: func(t *testing.T) []byte { return chatBody(t, true) },
			chunks: func(body []byte) []bodyChunk {
				mid := len(body) / 2
				return []bodyChunk{
					{data: body[:mid], end: false},
					{data: body[mid:], end: true},
				}
			},
		},
		{
			name: "streaming responses API",
			path: "/v1/responses",
			body: func(t *testing.T) []byte {
				t.Helper()
				body, err := json.Marshal(map[string]any{
					"model":  "gpt-4",
					"input":  "hello",
					"stream": true,
				})
				require.NoError(t, err)
				return body
			},
			chunks: func(body []byte) []bodyChunk {
				return []bodyChunk{{data: body, end: true}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			client, cleanup := dialExtProc(t, ctx, extProcServer)
			defer cleanup()

			body := tt.body(t)
			require.NoError(t, client.Send(requestHeaders(tt.path)))
			for _, chunk := range tt.chunks(body) {
				require.NoError(t, client.Send(requestBodyChunk(chunk.data, chunk.end)))
			}

			resp, err := client.Recv()
			require.NoError(t, err, "blocked input must return before the route timeout")

			ir := resp.GetImmediateResponse()
			require.NotNil(t, ir, "blocked input must be an ImmediateResponse, got %T", resp.GetResponse())
			assert.Equal(t, envoyTypePb.StatusCode_Forbidden, ir.GetStatus().GetCode())
			require.NotEmpty(t, ir.GetBody())
			assert.Equal(t, "application/json", immediateHeader(ir, "content-type"))
			assert.Equal(t, strconv.Itoa(len(ir.GetBody())), immediateHeader(ir, "content-length"))

			var parsed struct {
				Error struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(ir.GetBody(), &parsed))
			assert.Equal(t, refusal, parsed.Error.Message)
			assert.Equal(t, "content_filter", parsed.Error.Code)

			_, err = client.Recv()
			require.ErrorIs(t, err, io.EOF, "ext_proc stream must close after the block")
		})
	}
}

type bodyChunk struct {
	data []byte
	end  bool
}

func chatBody(t *testing.T, stream bool) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"stream": stream,
	})
	require.NoError(t, err)
	return body
}

func requestHeaders(path string) *extProcPb.ProcessingRequest {
	return &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{
				Headers: &corev3.HeaderMap{
					Headers: []*corev3.HeaderValue{
						{Key: ":path", RawValue: []byte(path)},
						{Key: "content-type", RawValue: []byte("application/json")},
					},
				},
			},
		},
	}
}

func requestBodyChunk(data []byte, end bool) *extProcPb.ProcessingRequest {
	return &extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{
				Body:        data,
				EndOfStream: end,
			},
		},
	}
}

func immediateHeader(ir *extProcPb.ImmediateResponse, key string) string {
	for _, h := range ir.GetHeaders().GetSetHeaders() {
		if h.GetHeader() == nil || h.Header.Key != key {
			continue
		}
		if len(h.Header.RawValue) > 0 {
			return string(h.Header.RawValue)
		}
		return h.Header.Value
	}
	return ""
}

func dialExtProc(t *testing.T, ctx context.Context, srv extProcPb.ExternalProcessorServer) (extProcPb.ExternalProcessor_ProcessClient, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(grpcSrv, srv)
	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	)
	require.NoError(t, err)

	client := extProcPb.NewExternalProcessorClient(conn)
	stream, err := client.Process(ctx)
	require.NoError(t, err)

	return stream, func() {
		grpcSrv.Stop()
		_ = conn.Close()
	}
}
