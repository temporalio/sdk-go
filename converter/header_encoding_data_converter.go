package converter

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// HeaderEncodingDataConverter opts into automatic codec-only processing of
// Temporal Payload headers, including tracing headers. Workers and replay inherit
// this policy from their configured DataConverter.
//
// Supplied headers must be serialized but not codec-encoded. Remove manual header
// codec processing before enabling this wrapper. Decoders must support existing
// histories; codec failures never fall back to plaintext. When using the gRPC
// codec interceptor, set SkipHeaders to avoid a second header codec pass.
// Nexus string headers and gRPC metadata are unaffected.
//
// Keep this wrapper outermost unless custom outer wrappers explicitly forward
// HeaderCodecProvider. Opaque wrappers can hide the opt-in; the SDK does not
// inspect their fields or invoke value conversion to discover codecs.
type HeaderEncodingDataConverter struct {
	DataConverter
	codec PayloadCodec
}

// NewHeaderEncodingDataConverter enables automatic header processing using
// delegate's codec stages. Missing or empty stages are rejected.
func NewHeaderEncodingDataConverter(delegate DataConverterWithCodec) (*HeaderEncodingDataConverter, error) {
	if delegate == nil {
		return nil, fmt.Errorf("header encoding requires a non-nil data converter with codec stages")
	}
	codec := delegate.Codec()
	if codec == nil {
		return nil, fmt.Errorf("header encoding requires non-nil payload codec stages")
	}
	return &HeaderEncodingDataConverter{DataConverter: delegate, codec: codec}, nil
}

// Codec returns the delegate's codec-only stages.
func (dc *HeaderEncodingDataConverter) Codec() PayloadCodec { return dc.codec }

// HeaderCodec enables automatic processing with the delegate's codec stages.
func (dc *HeaderEncodingDataConverter) HeaderCodec() PayloadCodec { return dc.codec }

// WithSerializationContext preserves value conversion context independently of
// header binding, so binding the value converter cannot encode headers twice.
func (dc *HeaderEncodingDataConverter) WithSerializationContext(ctx SerializationContext) DataConverter {
	result := *dc
	result.DataConverter = WithDataConverterSerializationContext(dc.DataConverter, ctx)
	if contextual, ok := dc.codec.(PayloadCodecWithSerializationContext); ok {
		result.codec = contextual.WithSerializationContext(ctx)
		if result.codec == nil {
			result.codec = payloadCodecChain{nil}
		}
	}
	return &result
}

// WithContext preserves context-aware value conversion.
func (dc *HeaderEncodingDataConverter) WithContext(ctx context.Context) DataConverter {
	result := *dc
	if delegate, ok := dc.DataConverter.(ContextAware); ok {
		result.DataConverter = delegate.WithContext(ctx)
	}
	return &result
}

// WithWorkflowContext preserves context-aware value conversion.
func (dc *HeaderEncodingDataConverter) WithWorkflowContext(ctx workflowcontext.Context) DataConverter {
	result := *dc
	if delegate, ok := dc.DataConverter.(ContextAware); ok {
		result.DataConverter = delegate.WithWorkflowContext(ctx)
	}
	return &result
}
