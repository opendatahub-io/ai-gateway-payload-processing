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
	"fmt"
	"os"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// grpcMaxRecvMsgSizeEnv overrides the ext-proc gRPC server receive limit.
	// Accepts a plain integer (bytes) or a Kubernetes quantity (e.g. 32Mi, 64M).
	grpcMaxRecvMsgSizeEnv = "GRPC_MAX_RECV_MSG_SIZE"

	// defaultGRPCMaxRecvMsgSize is sized for multimodal / agent chat payloads
	// (base64 images in conversation history) that routinely exceed gRPC's
	// compiled 4 MiB default, while still bounding memory vs math.MaxInt32.
	// 32 MiB matches common LLM-gateway / llm-d-router operator examples.
	// This is a per-gRPC-message cap, not an aggregate streamed-body budget.
	defaultGRPCMaxRecvMsgSize = 32 * 1024 * 1024
)

// resolveGRPCMaxRecvMsgSize returns the ext-proc gRPC MaxRecvMsgSize.
// When GRPC_MAX_RECV_MSG_SIZE is unset or empty, defaultGRPCMaxRecvMsgSize is used.
// Invalid values return an error so misconfiguration fails closed at startup.
func resolveGRPCMaxRecvMsgSize() (int, error) {
	return resolveGRPCMaxRecvMsgSizeFrom(os.Getenv(grpcMaxRecvMsgSizeEnv))
}

func resolveGRPCMaxRecvMsgSizeFrom(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultGRPCMaxRecvMsgSize, nil
	}

	n, err := parseMsgSizeBytes(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", grpcMaxRecvMsgSizeEnv, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s=%q: must be a positive size", grpcMaxRecvMsgSizeEnv, raw)
	}
	return n, nil
}

func parseMsgSizeBytes(raw string) (int, error) {
	// Prefer plain integers so operators can pass exact byte counts from logs
	// (e.g. the 4194304 from "received message larger than max").
	if n, err := strconv.Atoi(raw); err == nil {
		return n, nil
	}

	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return 0, fmt.Errorf("parse size: %w", err)
	}
	if q.Sign() <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}
	v := q.Value()
	if v > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("size %d overflows int", v)
	}
	return int(v), nil
}
