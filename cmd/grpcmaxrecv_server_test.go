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

package main

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const grpcCompiledMaxRecvDefault = 4 * 1024 * 1024

// TestExtProcMaxRecvAcceptsBodyAboveFourMiB is the RHAI-13005 regression:
// Envoy delivers a buffered request body as one gRPC message; with gRPC's
// compiled 4 MiB MaxRecvMsgSize the server returns RESOURCE_EXHAUSTED and the
// gateway emits an empty HTTP 500. Our default (32 MiB) must accept a body
// just over 4 MiB.
func TestExtProcMaxRecvAcceptsBodyAboveFourMiB(t *testing.T) {
	t.Parallel()

	maxRecv, err := resolveGRPCMaxRecvMsgSizeFrom("")
	require.NoError(t, err)
	require.Greater(t, maxRecv, grpcCompiledMaxRecvDefault)

	payload := make([]byte, grpcCompiledMaxRecvDefault+64*1024) // 4 MiB + 64 KiB
	for i := range payload {
		payload[i] = byte(i)
	}

	t.Run("default_limit_accepts", func(t *testing.T) {
		t.Parallel()
		size, err := echoOverGRPC(t, maxRecv, payload)
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), size)
	})

	t.Run("compiled_four_mib_rejects", func(t *testing.T) {
		t.Parallel()
		_, err := echoOverGRPC(t, grpcCompiledMaxRecvDefault, payload)
		require.Error(t, err)
		require.Equal(t, codes.ResourceExhausted, status.Code(err), "got: %v", err)
	})
}

func echoOverGRPC(t *testing.T, serverMaxRecv int, body []byte) (int64, error) {
	t.Helper()

	lis := bufconn.Listen(serverMaxRecv + len(body) + 1024)
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer(grpc.MaxRecvMsgSize(serverMaxRecv))
	srv.RegisterService(&echoBytesServiceDesc, echoBytesHandler{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	ctx := context.Background()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(len(body)+1024),
			grpc.MaxCallRecvMsgSize(1024),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	out := new(wrapperspb.Int64Value)
	err = conn.Invoke(ctx, "/test.EchoBytes/Echo", &wrapperspb.BytesValue{Value: body}, out)
	if err != nil {
		return 0, err
	}
	return out.GetValue(), nil
}

type echoBytesHandler struct{}

func (echoBytesHandler) Echo(_ context.Context, in *wrapperspb.BytesValue) (*wrapperspb.Int64Value, error) {
	return &wrapperspb.Int64Value{Value: int64(len(in.GetValue()))}, nil
}

type echoBytesServer interface {
	Echo(context.Context, *wrapperspb.BytesValue) (*wrapperspb.Int64Value, error)
}

var echoBytesServiceDesc = grpc.ServiceDesc{
	ServiceName: "test.EchoBytes",
	HandlerType: (*echoBytesServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Echo",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(wrapperspb.BytesValue)
				if err := dec(in); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return srv.(echoBytesServer).Echo(ctx, in)
				}
				info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/test.EchoBytes/Echo"}
				handler := func(ctx context.Context, req any) (any, error) {
					return srv.(echoBytesServer).Echo(ctx, req.(*wrapperspb.BytesValue))
				}
				return interceptor(ctx, in, info, handler)
			},
		},
	},
}
