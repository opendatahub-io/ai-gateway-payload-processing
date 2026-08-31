package dynamicmetadata

import (
	"encoding/json"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// Server wraps an ext-proc server to convert pseudo-headers set by plugins
// into ProcessingResponse.DynamicMetadata before the response reaches Envoy.
type Server struct {
	extProcPb.UnimplementedExternalProcessorServer
	inner extProcPb.ExternalProcessorServer
}

// WrapServer returns a Server that delegates to inner, injecting
// DynamicMetadata extracted from the pseudo-header on every Send.
func WrapServer(inner extProcPb.ExternalProcessorServer) extProcPb.ExternalProcessorServer {
	return &Server{inner: inner}
}

func (s *Server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	return s.inner.Process(&wrappedStream{ExternalProcessor_ProcessServer: srv})
}

type wrappedStream struct {
	extProcPb.ExternalProcessor_ProcessServer
}

func (w *wrappedStream) Send(resp *extProcPb.ProcessingResponse) error {
	extractAndInjectMetadata(resp)
	return w.ExternalProcessor_ProcessServer.Send(resp)
}

// commonResponse extracts the CommonResponse from any ProcessingResponse variant
// that carries header mutations (RequestHeaders, ResponseHeaders, RequestBody, ResponseBody).
func commonResponse(resp *extProcPb.ProcessingResponse) *extProcPb.CommonResponse {
	switch r := resp.Response.(type) {
	case *extProcPb.ProcessingResponse_RequestHeaders:
		if r.RequestHeaders != nil {
			return r.RequestHeaders.Response
		}
	case *extProcPb.ProcessingResponse_ResponseHeaders:
		if r.ResponseHeaders != nil {
			return r.ResponseHeaders.Response
		}
	case *extProcPb.ProcessingResponse_RequestBody:
		if r.RequestBody != nil {
			return r.RequestBody.Response
		}
	case *extProcPb.ProcessingResponse_ResponseBody:
		if r.ResponseBody != nil {
			return r.ResponseBody.Response
		}
	}
	return nil
}

// extractAndInjectMetadata finds the pseudo-header in the response's header
// mutations, removes it, and populates resp.DynamicMetadata. Scans every
// response variant (via commonResponse) so the internal pseudo-header is always
// stripped before it reaches Envoy/client, regardless of the phase a plugin set
// it on — hub mode's PROPOSE/TRANSFORM can emit it outside the request-headers
// phase.
func extractAndInjectMetadata(resp *extProcPb.ProcessingResponse) {
	cr := commonResponse(resp)
	if cr == nil || cr.HeaderMutation == nil || len(cr.HeaderMutation.SetHeaders) == 0 {
		return
	}

	hm := cr.HeaderMutation

	var e entry
	var parsed bool
	var pseudoFound bool
	filtered := make([]*corev3.HeaderValueOption, 0, len(hm.SetHeaders))
	for _, h := range hm.SetHeaders {
		if h.Header != nil && h.Header.Key == pseudoHeader {
			pseudoFound = true
			if err := json.Unmarshal([]byte(h.Header.GetRawValue()), &e); err == nil {
				parsed = true
			}
			continue
		}
		filtered = append(filtered, h)
	}

	if !pseudoFound {
		return
	}
	hm.SetHeaders = filtered

	if !parsed {
		return
	}

	listValues := make([]*structpb.Value, len(e.Values))
	for i, v := range e.Values {
		listValues[i] = structpb.NewStringValue(v)
	}

	resp.DynamicMetadata = &structpb.Struct{
		Fields: map[string]*structpb.Value{
			e.Namespace: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					e.Key: structpb.NewListValue(&structpb.ListValue{Values: listValues}),
				},
			}),
		},
	}
}
