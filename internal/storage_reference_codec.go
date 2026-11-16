package internal

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
)

type referenceSerializationContextKey struct{}

// withReferenceSerializationContext's sc must be the context used to encode the
// values the visited references replace.
func withReferenceSerializationContext(ctx context.Context, sc converter.SerializationContext) context.Context {
	return context.WithValue(ctx, referenceSerializationContextKey{}, sc)
}

func newReferenceCodecProvider(dc converter.DataConverter) extstore.ReferenceCodecProvider {
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}
	return func(ctx context.Context) extstore.ReferenceCodec {
		contextDC := WithContext(ctx, dc)
		if sc, ok := ctx.Value(referenceSerializationContextKey{}).(converter.SerializationContext); ok {
			contextDC = converter.WithDataConverterSerializationContext(contextDC, sc)
		}
		return dataConverterReferenceCodec{dataConverter: contextDC}
	}
}

// dataConverterReferenceCodec runs references through a DataConverter as raw
// values, which skips payload conversion but still applies its codecs.
type dataConverterReferenceCodec struct {
	dataConverter converter.DataConverter
}

func (c dataConverterReferenceCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	values := make([]any, len(payloads))
	for i, p := range payloads {
		values[i] = converter.NewRawValue(p)
	}
	encoded, err := c.dataConverter.ToPayloads(values...)
	if err != nil {
		return nil, err
	}
	return encoded.GetPayloads(), nil
}

func (c dataConverterReferenceCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	rawValues := make([]converter.RawValue, len(payloads))
	valuePtrs := make([]any, len(payloads))
	for i := range rawValues {
		valuePtrs[i] = &rawValues[i]
	}
	if err := c.dataConverter.FromPayloads(&commonpb.Payloads{Payloads: payloads}, valuePtrs...); err != nil {
		return nil, err
	}
	decoded := make([]*commonpb.Payload, len(rawValues))
	for i, v := range rawValues {
		decoded[i] = v.Payload()
	}
	return decoded, nil
}

func newInboundPayloadVisitor(dc converter.DataConverter, params extstore.StorageParameters) PayloadVisitor {
	return newCompositePayloadVisitor(
		extstore.NewReferenceDecodingVisitor(params, newReferenceCodecProvider(dc)),
		extstore.NewExternalRetrievalVisitor(params),
	)
}

func newOutboundStorageVisitor(dc converter.DataConverter, params extstore.StorageParameters) PayloadVisitor {
	if !extstore.WriteEncodedReferences {
		return extstore.NewExternalStorageVisitor(params)
	}
	return newCompositePayloadVisitor(
		extstore.NewExternalStorageVisitor(params),
		extstore.NewReferenceEncodingVisitor(newReferenceCodecProvider(dc)),
	)
}

// bindReferenceSerializationContext belongs wherever a DataConverter is bound to
// sc. An enclosing binding takes precedence, so a call site can override the
// context a stored visitor was bound with.
func bindReferenceSerializationContext(visitor PayloadVisitor, sc converter.SerializationContext) PayloadVisitor {
	if visitor == nil {
		return nil
	}
	return &referenceSerializationContextVisitor{inner: visitor, sc: sc}
}

type referenceSerializationContextVisitor struct {
	inner PayloadVisitor
	sc    converter.SerializationContext
}

func (v *referenceSerializationContextVisitor) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if _, ok := ctx.Value(referenceSerializationContextKey{}).(converter.SerializationContext); ok {
		return v.inner.Visit(ctx, payloads)
	}
	bound := *ctx
	bound.Context = withReferenceSerializationContext(ctx.Context, v.sc)
	return v.inner.Visit(&bound, payloads)
}

func (v *referenceSerializationContextVisitor) WithMessageLimit(ctx context.Context) context.Context {
	if limited, ok := v.inner.(PayloadVisitorWithMessageLimit); ok {
		return limited.WithMessageLimit(ctx)
	}
	return ctx
}

func (v *referenceSerializationContextVisitor) ContextHook(ctx context.Context, msg proto.Message) (context.Context, error) {
	if hookVisitor, ok := v.inner.(PayloadVisitorWithContextHook); ok {
		return hookVisitor.ContextHook(ctx, msg)
	}
	return ctx, nil
}
