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

// Package extproc finishes Envoy ext_proc local replies so a blocked request
// is a complete HTTP response instead of an empty status line.
package extproc

import (
	"encoding/json"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

const (
	headerContentType   = "content-type"
	headerContentLength = "content-length"
	contentTypeJSON     = "application/json"
)

// Server wraps an ext_proc server and completes every ImmediateResponse before
// it reaches Envoy.
type Server struct {
	extProcPb.UnimplementedExternalProcessorServer
	inner extProcPb.ExternalProcessorServer
}

// WrapServer returns a server that delegates to inner. ImmediateResponse
// messages are rewritten on Send so the local reply has a JSON body plus
// content-type and content-length. The inner handler still returns after that
// send, which closes the ext_proc stream.
func WrapServer(inner extProcPb.ExternalProcessorServer) extProcPb.ExternalProcessorServer {
	return &Server{inner: inner}
}

func (s *Server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	return s.inner.Process(&stream{ExternalProcessor_ProcessServer: srv})
}

type stream struct {
	extProcPb.ExternalProcessor_ProcessServer
}

func (w *stream) Send(resp *extProcPb.ProcessingResponse) error {
	completeImmediateResponse(resp)
	return w.ExternalProcessor_ProcessServer.Send(resp)
}

// apiError is the client body for a local reply. OpenAI-compatible callers
// display error.message; a plain-text body is treated as an empty failure.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// completeImmediateResponse fills in a local reply that Envoy can finish.
//
// IPP maps a plugin error to ImmediateResponse with a status and a plain-text
// body and no headers. On a FULL_DUPLEX_STREAMED gateway that local reply is
// what the client reads. Without content-type and content-length, streaming
// clients (chat completions with stream=true) receive the 403 and then wait
// for a body that never ends, until the route timeout. A JSON body with those
// two headers is a complete response: the client can display error.message and
// stop reading.
func completeImmediateResponse(resp *extProcPb.ProcessingResponse) {
	if resp == nil {
		return
	}
	ir := resp.GetImmediateResponse()
	if ir == nil {
		return
	}

	body := immediateBody(ir)
	ir.Body = body
	if ir.GetDetails() == "" {
		ir.Details = detailsForStatus(ir.GetStatus().GetCode())
	}
	if ir.Headers == nil {
		ir.Headers = &extProcPb.HeaderMutation{}
	}
	upsertHeader(ir.Headers, headerContentType, []byte(contentTypeJSON))
	upsertHeader(ir.Headers, headerContentLength, []byte(strconv.Itoa(len(body))))
}

func immediateBody(ir *extProcPb.ImmediateResponse) []byte {
	raw := ir.GetBody()
	if isJSONObject(raw) {
		return append([]byte(nil), raw...)
	}

	status := envoyTypePb.StatusCode(0)
	if ir.GetStatus() != nil {
		status = ir.GetStatus().GetCode()
	}
	message := clientMessage(raw, fallbackMessage(status))
	errType, code := errorClass(status)
	payload, err := json.Marshal(apiError{Error: apiErrorBody{
		Message: message,
		Type:    errType,
		Code:    code,
	}})
	if err != nil {
		payload = []byte(`{"error":{"message":"request failed","type":"server_error","code":"internal_error"}}`)
	}
	return payload
}

func clientMessage(body []byte, fallback string) string {
	text := strings.TrimSpace(string(body))
	const prefix = "inference error: "
	if strings.HasPrefix(text, prefix) {
		rest := strings.TrimPrefix(text, prefix)
		if _, msg, ok := strings.Cut(rest, " - "); ok {
			msg = strings.TrimSpace(msg)
			if msg != "" {
				return msg
			}
		}
	}
	if text != "" {
		return text
	}
	return fallback
}

func isJSONObject(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}

func fallbackMessage(code envoyTypePb.StatusCode) string {
	switch code {
	case envoyTypePb.StatusCode_Forbidden:
		return "forbidden"
	case envoyTypePb.StatusCode_BadRequest:
		return "bad request"
	case envoyTypePb.StatusCode_Unauthorized:
		return "unauthorized"
	case envoyTypePb.StatusCode_NotFound:
		return "not found"
	case envoyTypePb.StatusCode_TooManyRequests:
		return "rate limit exceeded"
	case envoyTypePb.StatusCode_ServiceUnavailable:
		return "service unavailable"
	default:
		return "request failed"
	}
}

func errorClass(code envoyTypePb.StatusCode) (errType, errCode string) {
	switch code {
	case envoyTypePb.StatusCode_Forbidden:
		return "invalid_request_error", "content_filter"
	case envoyTypePb.StatusCode_BadRequest:
		return "invalid_request_error", "bad_request"
	case envoyTypePb.StatusCode_Unauthorized:
		return "authentication_error", "unauthorized"
	case envoyTypePb.StatusCode_NotFound:
		return "invalid_request_error", "not_found"
	case envoyTypePb.StatusCode_TooManyRequests:
		return "rate_limit_error", "rate_limit_exceeded"
	case envoyTypePb.StatusCode_ServiceUnavailable:
		return "server_error", "service_unavailable"
	default:
		return "server_error", "internal_error"
	}
}

func detailsForStatus(code envoyTypePb.StatusCode) string {
	switch code {
	case envoyTypePb.StatusCode_Forbidden:
		return "forbidden"
	case envoyTypePb.StatusCode_BadRequest:
		return "bad_request"
	case envoyTypePb.StatusCode_Unauthorized:
		return "unauthorized"
	case envoyTypePb.StatusCode_NotFound:
		return "not_found"
	case envoyTypePb.StatusCode_TooManyRequests:
		return "too_many_requests"
	case envoyTypePb.StatusCode_ServiceUnavailable:
		return "service_unavailable"
	default:
		return "internal_error"
	}
}

func upsertHeader(hm *extProcPb.HeaderMutation, key string, value []byte) {
	for _, h := range hm.SetHeaders {
		if h.GetHeader() == nil || !strings.EqualFold(h.Header.Key, key) {
			continue
		}
		h.Header.Key = key
		h.Header.Value = ""
		h.Header.RawValue = value
		h.AppendAction = corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD
		return
	}
	hm.SetHeaders = append(hm.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      key,
			RawValue: value,
		},
		AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
	})
}
