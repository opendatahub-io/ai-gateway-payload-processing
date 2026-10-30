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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveGRPCMaxRecvMsgSizeFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "empty uses default", raw: "", want: defaultGRPCMaxRecvMsgSize},
		{name: "whitespace uses default", raw: "  \t ", want: defaultGRPCMaxRecvMsgSize},
		{name: "plain bytes", raw: "67108864", want: 64 * 1024 * 1024},
		{name: "binary MiB", raw: "32Mi", want: 32 * 1024 * 1024},
		{name: "binary GiB", raw: "1Gi", want: 1024 * 1024 * 1024},
		{name: "decimal MB", raw: "64M", want: 64 * 1000 * 1000},
		{name: "zero rejected", raw: "0", wantErr: true},
		{name: "negative rejected", raw: "-1", wantErr: true},
		{name: "garbage rejected", raw: "lots", wantErr: true},
		{name: "empty quantity rejected", raw: "Mi", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveGRPCMaxRecvMsgSizeFrom(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultGRPCMaxRecvMsgSizeAboveGRPCDefault(t *testing.T) {
	t.Parallel()
	const grpcCompiledDefault = 4 * 1024 * 1024
	require.Greater(t, defaultGRPCMaxRecvMsgSize, grpcCompiledDefault)
}
