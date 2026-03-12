package converter

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

// signingCodec adds a context-derived signature on Encode, verifies on Decode.
type signingCodec struct {
	signature string
}

func (c *signingCodec) WithSerializationContext(ctx SerializationContext) PayloadCodec {
	switch sc := ctx.(type) {
	case WorkflowSerializationContext:
		return &signingCodec{signature: sc.WorkflowID}
	case ActivitySerializationContext:
		return &signingCodec{signature: sc.WorkflowID + ":" + sc.ActivityType}
	}
	return c
}

func (c *signingCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		clone := proto.Clone(p).(*commonpb.Payload)
		if clone.Metadata == nil {
			clone.Metadata = map[string][]byte{}
		}
		clone.Metadata["ctx-signature"] = []byte(c.signature)
		result[i] = clone
	}
	return result, nil
}

func (c *signingCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		sig := string(p.Metadata["ctx-signature"])
		if sig != c.signature {
			return nil, fmt.Errorf("signature mismatch: got %q, want %q", sig, c.signature)
		}
		clone := proto.Clone(p).(*commonpb.Payload)
		delete(clone.Metadata, "ctx-signature")
		result[i] = clone
	}
	return result, nil
}

// capturingDC records every SerializationContext it receives.
type capturingDC struct {
	DataConverter
	mu       *sync.Mutex
	contexts *[]SerializationContext
}

func newCapturingDC() *capturingDC {
	contexts := make([]SerializationContext, 0)
	mu := &sync.Mutex{}
	return &capturingDC{
		DataConverter: GetDefaultDataConverter(),
		mu:            mu,
		contexts:      &contexts,
	}
}

func (dc *capturingDC) WithSerializationContext(ctx SerializationContext) DataConverter {
	dc.mu.Lock()
	*dc.contexts = append(*dc.contexts, ctx)
	dc.mu.Unlock()
	return &capturingDC{
		DataConverter: dc.DataConverter,
		mu:            dc.mu,
		contexts:      dc.contexts,
	}
}

func (dc *capturingDC) getCapturedContexts() []SerializationContext {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	out := make([]SerializationContext, len(*dc.contexts))
	copy(out, *dc.contexts)
	return out
}

func TestCodecDataConverter_PropagatesBothDCAndCodec(t *testing.T) {
	require := require.New(t)
	parentDC := newCapturingDC()
	codec := &signingCodec{}
	cdc := NewCodecDataConverter(parentDC, codec)

	ctx := WorkflowSerializationContext{Namespace: "ns", WorkflowID: "wf-1"}
	wrapped := WithSerializationContext(cdc, ctx)

	require.NotSame(cdc, wrapped)

	captured := parentDC.getCapturedContexts()
	require.Len(captured, 1)
	require.Equal(ctx, captured[0])

	// Round-trip works with context-aware parent and codec
	payload, err := wrapped.ToPayload("hello")
	require.NoError(err)
	require.Equal("wf-1", string(payload.Metadata["ctx-signature"]))

	var result string
	require.NoError(wrapped.FromPayload(payload, &result))
	require.Equal("hello", result)
}

func TestCodecDataConverter_PropagatesCodecOnly(t *testing.T) {
	require := require.New(t)
	parentDC := GetDefaultDataConverter()
	codec := &signingCodec{}
	cdc := NewCodecDataConverter(parentDC, codec)

	ctx := WorkflowSerializationContext{WorkflowID: "wf-2"}
	wrapped := WithSerializationContext(cdc, ctx)

	require.NotSame(cdc, wrapped)

	payload, err := wrapped.ToPayload("test")
	require.NoError(err)
	require.Equal("wf-2", string(payload.Metadata["ctx-signature"]))

	var result string
	require.NoError(wrapped.FromPayload(payload, &result))
	require.Equal("test", result)
}

func TestCodecDataConverter_PropagatesDCOnly(t *testing.T) {
	require := require.New(t)
	parentDC := newCapturingDC()
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	cdc := NewCodecDataConverter(parentDC, codec)

	ctx := WorkflowSerializationContext{WorkflowID: "wf-3"}
	wrapped := WithSerializationContext(cdc, ctx)

	require.NotSame(cdc, wrapped)

	captured := parentDC.getCapturedContexts()
	require.Len(captured, 1)
	require.Equal(ctx, captured[0])
}

func TestCodecDataConverter_NeitherImplements(t *testing.T) {
	require := require.New(t)
	parentDC := GetDefaultDataConverter()
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	cdc := NewCodecDataConverter(parentDC, codec)

	ctx := WorkflowSerializationContext{WorkflowID: "wf-4"}
	wrapped := WithSerializationContext(cdc, ctx)

	require.Same(cdc, wrapped)
}

func TestCodecDataConverter_SigningRoundTrip(t *testing.T) {
	require := require.New(t)
	codec := &signingCodec{}
	cdc := NewCodecDataConverter(GetDefaultDataConverter(), codec)

	wrapped1 := WithSerializationContext(cdc, WorkflowSerializationContext{WorkflowID: "wf-A"})
	payload, err := wrapped1.ToPayload("hello")
	require.NoError(err)

	// Same context decodes successfully
	var result string
	require.NoError(wrapped1.FromPayload(payload, &result))
	require.Equal("hello", result)

	// Different context fails to decode
	wrapped2 := WithSerializationContext(cdc, WorkflowSerializationContext{WorkflowID: "wf-B"})
	err = wrapped2.FromPayload(payload, &result)
	require.Error(err)
	require.Contains(err.Error(), "signature mismatch")
}

func TestCodecDataConverter_SigningRoundTrip_ActivityContext(t *testing.T) {
	require := require.New(t)
	codec := &signingCodec{}
	cdc := NewCodecDataConverter(GetDefaultDataConverter(), codec)

	actCtx := ActivitySerializationContext{WorkflowID: "wf-1", ActivityType: "MyActivity"}
	wrapped := WithSerializationContext(cdc, actCtx)
	payload, err := wrapped.ToPayload("data")
	require.NoError(err)
	require.Equal("wf-1:MyActivity", string(payload.Metadata["ctx-signature"]))

	var result string
	require.NoError(wrapped.FromPayload(payload, &result))
	require.Equal("data", result)

	// Different activity type fails
	otherCtx := ActivitySerializationContext{WorkflowID: "wf-1", ActivityType: "OtherActivity"}
	wrapped2 := WithSerializationContext(cdc, otherCtx)
	err = wrapped2.FromPayload(payload, &result)
	require.Error(err)
	require.Contains(err.Error(), "signature mismatch")
}

func TestWithSerializationContextHelper_NoOp(t *testing.T) {
	dc := GetDefaultDataConverter()
	result := WithSerializationContext(dc, WorkflowSerializationContext{WorkflowID: "wf"})
	require.Same(t, dc, result)
}

func TestWithSerializationContextHelper_Wraps(t *testing.T) {
	dc := newCapturingDC()
	ctx := WorkflowSerializationContext{WorkflowID: "wf"}
	result := WithSerializationContext(dc, ctx)
	require.NotSame(t, dc, result)

	captured := dc.getCapturedContexts()
	require.Len(t, captured, 1)
	require.Equal(t, ctx, captured[0])
}
