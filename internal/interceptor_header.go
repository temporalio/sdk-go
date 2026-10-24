package internal

import (
	"context"
	"fmt"
	"maps"
	"slices"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	"google.golang.org/protobuf/proto"
)

type headerKey struct{}

func headerCodecFromDataConverter(dc converter.DataConverter) converter.PayloadCodec {
	provider, ok := dc.(converter.HeaderCodecProvider)
	if !ok {
		return nil
	}
	return provider.HeaderCodec()
}

func bindHeaderCodec(codec converter.PayloadCodec, sc converter.SerializationContext) converter.PayloadCodec {
	if contextual, ok := codec.(converter.PayloadCodecWithSerializationContext); ok {
		bound := contextual.WithSerializationContext(sc)
		if bound == nil {
			return headerCodecBindingError{fmt.Errorf("header codec returned nil from WithSerializationContext")}
		}
		return bound
	}
	return codec
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
	if codec == nil || len(header.GetFields()) == 0 {
		return header, nil
	}
	result := proto.Clone(header).(*commonpb.Header)
	keys := make([]string, 0, len(result.Fields))
	for key := range result.Fields {
		keys = append(keys, key)
	}
	// Codecs may be context-aware or stateful, so workflow replay must visit
	// headers in the same order regardless of Go's map iteration order.
	slices.Sort(keys)
	for _, key := range keys {
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
func Header(ctx context.Context) map[string]*commonpb.Payload {
	m, _ := ctx.Value(headerKey{}).(map[string]*commonpb.Payload)
	return m
}

func contextWithNewHeader(ctx context.Context) context.Context {
	return context.WithValue(ctx, headerKey{}, map[string]*commonpb.Payload{})
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
	header, err := codecHeader(header, codec, false)
	if err != nil {
		return nil, err
	}
	if header == nil {
		header = &commonpb.Header{}
	}
	if header.Fields == nil {
		header.Fields = map[string]*commonpb.Payload{}
	}
	reader := NewHeaderReader(header)
	for _, ctxProp := range ctxProps {
		var err error
		if ctx, err = ctxProp.Extract(ctx, reader); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	return context.WithValue(ctx, headerKey{}, header.Fields), nil
}

func headerPropagated(ctx context.Context, ctxProps []ContextPropagator, codec converter.PayloadCodec) (*commonpb.Header, error) {
	header := &commonpb.Header{Fields: Header(ctx)}
	if header.Fields == nil {
		return nil, fmt.Errorf("context missing header")
	}
	if codec != nil {
		header.Fields = maps.Clone(header.Fields)
	}
	writer := NewHeaderWriter(header)
	for _, ctxProp := range ctxProps {
		if err := ctxProp.Inject(ctx, writer); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	return codecHeader(header, codec, true)
}

// WorkflowHeader provides Temporal header information from the workflow context
// for reading or writing during specific interceptor calls. See documentation
// in the interceptor package for more details.
//
// Exposed as: [go.temporal.io/sdk/interceptor.WorkflowHeader]
func WorkflowHeader(ctx Context) map[string]*commonpb.Payload {
	m, _ := ctx.Value(headerKey{}).(map[string]*commonpb.Payload)
	return m
}

func workflowContextWithNewHeader(ctx Context) Context {
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
	header, err := codecHeader(header, workflowHeaderCodec(ctx, nil), false)
	if err != nil {
		return nil, err
	}
	if header == nil {
		header = &commonpb.Header{}
	}
	if header.Fields == nil {
		header.Fields = map[string]*commonpb.Payload{}
	}
	reader := NewHeaderReader(header)
	for _, ctxProp := range ctxProps {
		var err error
		if ctx, err = ctxProp.ExtractToWorkflow(ctx, reader); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	return WithValue(ctx, headerKey{}, header.Fields), nil
}

func workflowHeaderPropagated(ctx Context, ctxProps []ContextPropagator, sc converter.SerializationContext) (*commonpb.Header, error) {
	header := &commonpb.Header{Fields: WorkflowHeader(ctx)}
	if header.Fields == nil {
		return nil, fmt.Errorf("context missing workflow header")
	}
	codec := workflowHeaderCodec(ctx, sc)
	if codec != nil {
		header.Fields = maps.Clone(header.Fields)
	}
	writer := NewHeaderWriter(header)
	for _, ctxProp := range ctxProps {
		if err := ctxProp.InjectFromWorkflow(ctx, writer); err != nil {
			return nil, fmt.Errorf("failed propagating header: %w", err)
		}
	}
	return codecHeader(header, codec, true)
}
