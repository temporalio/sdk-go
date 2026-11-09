package internal

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	"google.golang.org/protobuf/proto"
)

type headerKey struct{}
type headerEncodingKeys struct{}
type headerDecodingCodec struct{}

type headerContext interface{ Value(any) any }

func headerCodecFromDataConverter(dc converter.DataConverter) converter.PayloadCodec {
	provider, ok := dc.(converter.PayloadCodecProvider)
	if !ok {
		return nil
	}
	return provider.Codec()
}

func bindHeaderCodec(codec converter.PayloadCodec, sc converter.SerializationContext) converter.PayloadCodec {
	if contextual, ok := codec.(converter.PayloadCodecWithSerializationContext); ok {
		return contextualHeaderCodec(sync.OnceValue(func() converter.PayloadCodec {
			bound := contextual.WithSerializationContext(sc)
			if bound == nil {
				return headerCodecBindingError{fmt.Errorf("header codec returned nil from WithSerializationContext")}
			}
			return bound
		}))
	}
	return codec
}

type contextualHeaderCodec func() converter.PayloadCodec

func (c contextualHeaderCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return c().Encode(payloads)
}

func (c contextualHeaderCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return c().Decode(payloads)
}

type headerCodecBindingError struct{ err error }

func (c headerCodecBindingError) Encode([]*commonpb.Payload) ([]*commonpb.Payload, error) {
	return nil, c.err
}

func (c headerCodecBindingError) Decode([]*commonpb.Payload) ([]*commonpb.Payload, error) {
	return nil, c.err
}

func workflowHeaderCodec(ctx Context, sc converter.SerializationContext) converter.PayloadCodec {
	options := getWorkflowEnvOptions(ctx)
	if options == nil || options.headerCodec == nil {
		return nil
	}
	if sc == nil {
		info := GetWorkflowInfo(ctx)
		sc = converter.WorkflowSerializationContext{Namespace: info.Namespace, WorkflowID: info.WorkflowExecution.ID}
	}
	codec := options.headerCodec
	// The stored codec predates workflow execution, so its exemption needs the
	// running context before either context binding or codec calls can pause detection.
	if contextual, ok := codec.(commonconverter.WorkflowContextAwarePayloadCodec); ok {
		codec = contextual.WithWorkflowContext(ctx)
		if codec == nil {
			return headerCodecBindingError{fmt.Errorf("header codec returned nil from WithWorkflowContext")}
		}
	}
	return bindHeaderCodec(codec, sc)
}

func (wc *WorkflowClient) workflowHeaderCodec(workflowID string) converter.PayloadCodec {
	return bindHeaderCodec(wc.headerCodec, converter.WorkflowSerializationContext{
		Namespace: wc.namespace, WorkflowID: workflowID,
	})
}

func codecHeader(header *commonpb.Header, codec converter.PayloadCodec, encode bool) (*commonpb.Header, error) {
	if header == nil {
		return nil, nil
	}
	if codec == nil || len(header.GetFields()) == 0 {
		if !encode {
			return proto.Clone(header).(*commonpb.Header), nil
		}
		return header, nil
	}
	result := proto.Clone(header).(*commonpb.Header)
	// Codecs may be context-aware or stateful, so workflow replay must visit
	// headers in the same order regardless of Go's map iteration order.
	for _, key := range slices.Sorted(maps.Keys(result.Fields)) {
		payloads := []*commonpb.Payload{result.Fields[key]}
		var err error
		if encode {
			payloads, err = codec.Encode(payloads)
		} else {
			payloads, err = codec.Decode(payloads)
		}
		if err != nil {
			return nil, fmt.Errorf("header %q codec failed: %w", key, err)
		}
		if len(payloads) != 1 {
			return nil, fmt.Errorf("header %q codec returned %d payloads, expected 1", key, len(payloads))
		}
		result.Fields[key] = payloads[0]
	}
	return result, nil
}

// Header provides Temporal header information from the context for reading or
// writing during specific interceptor calls. See documentation in the
// interceptor package for more details.
//
// Exposed as: [go.temporal.io/sdk/interceptor.Header]
func Header(ctx headerContext) map[string]*commonpb.Payload {
	m, _ := ctx.Value(headerKey{}).(map[string]*commonpb.Payload)
	return m
}

func contextWithNewHeader(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, headerEncodingKeys{}, map[string]*commonpb.Payload{})
	return context.WithValue(ctx, headerKey{}, map[string]*commonpb.Payload{})
}

// EncodeHeader marks a tracing producer's output for codec processing once the
// destination serialization context is known at the request boundary.
// The mark belongs to the key and payload instance, even if removed and reinserted.
func EncodeHeader(ctx headerContext, key string) {
	if keys, ok := ctx.Value(headerEncodingKeys{}).(map[string]*commonpb.Payload); ok {
		keys[key] = Header(ctx)[key]
	}
}

// DecodedHeader returns a decoded copy without modifying the context's wire headers.
func DecodedHeader(ctx headerContext) (map[string]*commonpb.Payload, error) {
	codec, _ := ctx.Value(headerDecodingCodec{}).(converter.PayloadCodec)
	if workflowCtx, ok := ctx.(Context); ok {
		codec = workflowHeaderCodec(workflowCtx, nil)
	}
	header, err := codecHeader(&commonpb.Header{Fields: Header(ctx)}, codec, false)
	if err != nil {
		return nil, err
	}
	return header.Fields, nil
}

func propagatedHeaders(
	ctx headerContext, propagators []ContextPropagator, codec converter.PayloadCodec,
	inject func(ContextPropagator, HeaderWriter) error,
) (*commonpb.Header, error) {
	header := &commonpb.Header{Fields: Header(ctx)}
	if header.Fields == nil {
		return nil, fmt.Errorf("context missing header")
	}
	marked, _ := ctx.Value(headerEncodingKeys{}).(map[string]*commonpb.Payload)
	owned := make(map[string]*commonpb.Payload, len(marked))
	maps.Copy(owned, marked)
	enabled := make([]bool, len(propagators))
	copyHeaders := len(owned) != 0
	for i, propagator := range propagators {
		enabled[i] = propagatorCodecEnabled(propagator)
		copyHeaders = copyHeaders || enabled[i]
	}
	if copyHeaders {
		header.Fields = maps.Clone(header.Fields)
	}
	writer := &headerWriter{header: header, codecValues: owned}
	for i, propagator := range propagators {
		writer.encode = enabled[i]
		if err := inject(propagator, writer); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	selected := &commonpb.Header{Fields: map[string]*commonpb.Payload{}}
	for key, value := range owned {
		if payload, ok := header.Fields[key]; ok && payload == value {
			selected.Fields[key] = payload
		}
	}
	encoded, err := codecHeader(selected, codec, true)
	if err != nil {
		return nil, err
	}
	maps.Copy(header.Fields, encoded.Fields)
	return header, nil
}

func propagatorCodecEnabled(propagator ContextPropagator) bool {
	p, ok := propagator.(CodecContextPropagator)
	return ok && p.EncodeHeaders()
}

func contextWithoutHeader(ctx context.Context) context.Context {
	return context.WithValue(ctx, headerKey{}, nil)
}

func contextWithHeaderPropagated(
	ctx context.Context,
	header *commonpb.Header,
	ctxProps []ContextPropagator,
	codec converter.PayloadCodec,
) (context.Context, error) {
	if header == nil {
		header = &commonpb.Header{}
	}
	if header.Fields == nil {
		header.Fields = map[string]*commonpb.Payload{}
	}
	for _, ctxProp := range ctxProps {
		view := header
		if propagatorCodecEnabled(ctxProp) {
			var err error
			view, err = codecHeader(header, codec, false)
			if err != nil {
				return nil, err
			}
		}
		var err error
		if ctx, err = ctxProp.Extract(ctx, NewHeaderReader(view)); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	ctx = context.WithValue(ctx, headerDecodingCodec{}, codec)
	return context.WithValue(ctx, headerKey{}, header.Fields), nil
}

func headerPropagated(ctx context.Context, ctxProps []ContextPropagator, codec converter.PayloadCodec) (*commonpb.Header, error) {
	return propagatedHeaders(ctx, ctxProps, codec, func(p ContextPropagator, writer HeaderWriter) error {
		return p.Inject(ctx, writer)
	})
}

// WorkflowHeader provides Temporal header information from the workflow context
// for reading or writing during specific interceptor calls. See documentation
// in the interceptor package for more details.
//
// Exposed as: [go.temporal.io/sdk/interceptor.WorkflowHeader]
func WorkflowHeader(ctx Context) map[string]*commonpb.Payload {
	return Header(ctx)
}

func workflowContextWithNewHeader(ctx Context) Context {
	ctx = WithValue(ctx, headerEncodingKeys{}, map[string]*commonpb.Payload{})
	return WithValue(ctx, headerKey{}, map[string]*commonpb.Payload{})
}

func workflowContextWithoutHeader(ctx Context) Context {
	return WithValue(ctx, headerKey{}, nil)
}

func workflowContextWithHeaderPropagated(
	ctx Context,
	header *commonpb.Header,
	ctxProps []ContextPropagator,
) (Context, error) {
	if header == nil {
		header = &commonpb.Header{}
	}
	if header.Fields == nil {
		header.Fields = map[string]*commonpb.Payload{}
	}
	for _, ctxProp := range ctxProps {
		view := header
		if propagatorCodecEnabled(ctxProp) {
			var err error
			view, err = codecHeader(header, workflowHeaderCodec(ctx, nil), false)
			if err != nil {
				return nil, err
			}
		}
		var err error
		if ctx, err = ctxProp.ExtractToWorkflow(ctx, NewHeaderReader(view)); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	return WithValue(ctx, headerKey{}, header.Fields), nil
}

func workflowHeaderPropagated(ctx Context, ctxProps []ContextPropagator, sc converter.SerializationContext) (*commonpb.Header, error) {
	return propagatedHeaders(ctx, ctxProps, workflowHeaderCodec(ctx, sc), func(p ContextPropagator, writer HeaderWriter) error {
		return p.InjectFromWorkflow(ctx, writer)
	})
}
