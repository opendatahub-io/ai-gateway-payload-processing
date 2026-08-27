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

// extractAndInjectMetadata finds the pseudo-header in the response's header
// mutations, removes it, and populates resp.DynamicMetadata.
//
// Only the RequestHeaders response variant is scanned. This is deliberate:
// plugins set the pseudo-header during the request-headers phase, so it can
// only ever ride a ProcessingResponse_RequestHeaders. Scanning that one variant
// also guarantees the pseudo-header is stripped before it reaches Envoy/client.
// If a plugin ever sets the pseudo-header on another phase (e.g. ResponseHeaders
// or ResponseBody), this scan must be generalized to those variants — otherwise
// the raw pseudo-header would leak downstream instead of becoming DynamicMetadata.
func extractAndInjectMetadata(resp *extProcPb.ProcessingResponse) {
	reqHeaders, ok := resp.Response.(*extProcPb.ProcessingResponse_RequestHeaders)
	if !ok || reqHeaders.RequestHeaders == nil ||
		reqHeaders.RequestHeaders.Response == nil ||
		reqHeaders.RequestHeaders.Response.HeaderMutation == nil {
		return
	}

	hm := reqHeaders.RequestHeaders.Response.HeaderMutation
	if len(hm.SetHeaders) == 0 {
		return
	}

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
