package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type stringTransferValue struct {
	Value string
}

func (stringTransferValue) TransferTypeConverter() converter.TransferTypeConverter {
	return stringTransferTypeConverter{}
}

type stringTransferTypeConverter struct{}

func (stringTransferTypeConverter) NewTransferType() any {
	return new(string)
}

func (stringTransferTypeConverter) ToTransferType(value any) (any, error) {
	switch value := value.(type) {
	case stringTransferValue:
		return value.Value, nil
	case *stringTransferValue:
		return value.Value, nil
	default:
		return nil, fmt.Errorf("expected stringTransferValue or *stringTransferValue, got %T", value)
	}
}

func (stringTransferTypeConverter) FromTransferType(value any) (any, error) {
	transferValue, ok := value.(*string)
	if !ok {
		return nil, fmt.Errorf("expected *string, got %T", value)
	}
	return stringTransferValue{Value: *transferValue}, nil
}

type protoTransferValue struct {
	Value string
}

func (protoTransferValue) TransferTypeConverter() converter.TransferTypeConverter {
	return protoTransferTypeConverter{}
}

type protoTransferTypeConverter struct{}

func (protoTransferTypeConverter) NewTransferType() any {
	return new(wrapperspb.StringValue)
}

func (protoTransferTypeConverter) ToTransferType(value any) (any, error) {
	switch value := value.(type) {
	case protoTransferValue:
		return wrapperspb.String(value.Value), nil
	case *protoTransferValue:
		return wrapperspb.String(value.Value), nil
	default:
		return nil, fmt.Errorf("expected protoTransferValue or *protoTransferValue, got %T", value)
	}
}

func (protoTransferTypeConverter) FromTransferType(value any) (any, error) {
	transferValue, ok := value.(*wrapperspb.StringValue)
	if !ok {
		return nil, fmt.Errorf("expected *wrapperspb.StringValue, got %T", value)
	}
	return protoTransferValue{Value: transferValue.Value}, nil
}

type pointerMarkerValue struct {
	Value string
}

func (*pointerMarkerValue) TransferTypeConverter() converter.TransferTypeConverter {
	return pointerMarkerTransferTypeConverter{}
}

type pointerMarkerTransferTypeConverter struct{}

func (pointerMarkerTransferTypeConverter) NewTransferType() any {
	return new(string)
}

func (pointerMarkerTransferTypeConverter) ToTransferType(value any) (any, error) {
	transferValue, ok := value.(*pointerMarkerValue)
	if !ok {
		return nil, fmt.Errorf("expected *pointerMarkerValue, got %T", value)
	}
	return transferValue.Value, nil
}

func (pointerMarkerTransferTypeConverter) FromTransferType(value any) (any, error) {
	transferValue, ok := value.(*string)
	if !ok {
		return nil, fmt.Errorf("expected *string, got %T", value)
	}
	return pointerMarkerValue{Value: *transferValue}, nil
}

type pointerDestinationValue struct {
	Value string
}

func (*pointerDestinationValue) TransferTypeConverter() converter.TransferTypeConverter {
	return pointerDestinationTransferTypeConverter{}
}

type pointerDestinationTransferTypeConverter struct{}

func (pointerDestinationTransferTypeConverter) NewTransferType() any {
	return new(string)
}

func (pointerDestinationTransferTypeConverter) ToTransferType(value any) (any, error) {
	transferValue, ok := value.(*pointerDestinationValue)
	if !ok {
		return nil, fmt.Errorf("expected *pointerDestinationValue, got %T", value)
	}
	return transferValue.Value, nil
}

func (pointerDestinationTransferTypeConverter) FromTransferType(value any) (any, error) {
	transferValue, ok := value.(*string)
	if !ok {
		return nil, fmt.Errorf("expected *string, got %T", value)
	}
	return &pointerDestinationValue{Value: *transferValue}, nil
}

type configurableTransferValue struct {
	Value             string
	TransferConverter converter.TransferTypeConverter `json:"-"`
}

func (value configurableTransferValue) TransferTypeConverter() converter.TransferTypeConverter {
	return value.TransferConverter
}

type testTransferTypeConverter struct {
	newTransferTypeFn  func() any
	toTransferTypeFn   func(any) (any, error)
	fromTransferTypeFn func(any) (any, error)
}

func (c *testTransferTypeConverter) NewTransferType() any {
	return c.newTransferTypeFn()
}

func (c *testTransferTypeConverter) ToTransferType(value any) (any, error) {
	return c.toTransferTypeFn(value)
}

func (c *testTransferTypeConverter) FromTransferType(value any) (any, error) {
	return c.fromTransferTypeFn(value)
}

type nilableTransferMap map[string]string

func (nilableTransferMap) TransferTypeConverter() converter.TransferTypeConverter {
	return nilResultTransferTypeConverter{}
}

type nilResultTransferTypeConverter struct{}

func (nilResultTransferTypeConverter) NewTransferType() any {
	return new(string)
}

func (nilResultTransferTypeConverter) ToTransferType(any) (any, error) {
	return "", nil
}

func (nilResultTransferTypeConverter) FromTransferType(any) (any, error) {
	return nil, nil
}

type panicValueReceiverMarker struct{}

func (panicValueReceiverMarker) TransferTypeConverter() converter.TransferTypeConverter {
	panic("TransferTypeConverter must not be called")
}

type topLevelOnlyValue struct {
	Value string `json:"value"`
	calls *int
}

func (value topLevelOnlyValue) TransferTypeConverter() converter.TransferTypeConverter {
	return topLevelOnlyTransferTypeConverter{calls: value.calls}
}

type topLevelOnlyTransferTypeConverter struct {
	calls *int
}

func (topLevelOnlyTransferTypeConverter) NewTransferType() any {
	return new(string)
}

func (c topLevelOnlyTransferTypeConverter) ToTransferType(value any) (any, error) {
	if c.calls != nil {
		(*c.calls)++
	}
	switch value := value.(type) {
	case topLevelOnlyValue:
		return value.Value, nil
	case *topLevelOnlyValue:
		return value.Value, nil
	default:
		return nil, fmt.Errorf("expected topLevelOnlyValue or *topLevelOnlyValue, got %T", value)
	}
}

func (topLevelOnlyTransferTypeConverter) FromTransferType(value any) (any, error) {
	transferValue, ok := value.(*string)
	if !ok {
		return nil, fmt.Errorf("expected *string, got %T", value)
	}
	return topLevelOnlyValue{Value: *transferValue}, nil
}

type topLevelOnlyContainer struct {
	Nested topLevelOnlyValue `json:"nested"`
}

type plainTransferTestValue struct {
	Number int
}

var (
	_ converter.TransferTypeConvertible = stringTransferValue{}
	_ converter.TransferTypeConvertible = (*stringTransferValue)(nil)
	_ converter.TransferTypeConvertible = (*pointerMarkerValue)(nil)
	_ converter.TransferTypeConverter   = stringTransferTypeConverter{}
	_ converter.TransferTypeConverter   = (*testTransferTypeConverter)(nil)
)

type recordingDataConverter struct {
	converter.DataConverter

	toPayloadCalls       int
	toPayloadValues      []any
	toPayloadFn          func(any) (*commonpb.Payload, error)
	toPayloadsCalls      int
	toPayloadsValues     []any
	toPayloadsFn         func(...any) (*commonpb.Payloads, error)
	fromPayloadCalls     int
	fromPayloadPayload   *commonpb.Payload
	fromPayloadValuePtr  any
	fromPayloadFn        func(*commonpb.Payload, any) error
	fromPayloadsCalls    int
	fromPayloadsPayloads *commonpb.Payloads
	fromPayloadsValuePtr []any
	fromPayloadsFn       func(*commonpb.Payloads, ...any) error
}

func newRecordingDataConverter() *recordingDataConverter {
	return &recordingDataConverter{DataConverter: converter.GetDefaultDataConverter()}
}

func (dc *recordingDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	dc.toPayloadCalls++
	dc.toPayloadValues = append(dc.toPayloadValues, value)
	if dc.toPayloadFn != nil {
		return dc.toPayloadFn(value)
	}
	return dc.DataConverter.ToPayload(value)
}

func (dc *recordingDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	dc.toPayloadsCalls++
	dc.toPayloadsValues = append([]any(nil), values...)
	if dc.toPayloadsFn != nil {
		return dc.toPayloadsFn(values...)
	}
	return dc.DataConverter.ToPayloads(values...)
}

func (dc *recordingDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	dc.fromPayloadCalls++
	dc.fromPayloadPayload = payload
	dc.fromPayloadValuePtr = valuePtr
	if dc.fromPayloadFn != nil {
		return dc.fromPayloadFn(payload, valuePtr)
	}
	return dc.DataConverter.FromPayload(payload, valuePtr)
}

func (dc *recordingDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	dc.fromPayloadsCalls++
	dc.fromPayloadsPayloads = payloads
	dc.fromPayloadsValuePtr = append([]any(nil), valuePtrs...)
	if dc.fromPayloadsFn != nil {
		return dc.fromPayloadsFn(payloads, valuePtrs...)
	}
	return dc.DataConverter.FromPayloads(payloads, valuePtrs...)
}

type boundaryDataConverter struct {
	toPayloadCalls    int
	toPayloadValue    any
	toPayloadsCalls   int
	toPayloadsValues  []any
	fromPayloadCalls  int
	fromPayloadsCalls int
	toStringCalls     int
	toStringsCalls    int
	toStringPayload   *commonpb.Payload
	toStringsPayloads *commonpb.Payloads
}

func (dc *boundaryDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	dc.toPayloadCalls++
	dc.toPayloadValue = value
	return &commonpb.Payload{Data: []byte("single encode")}, nil
}

func (dc *boundaryDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	dc.toPayloadsCalls++
	dc.toPayloadsValues = append([]any(nil), values...)
	payloads := make([]*commonpb.Payload, len(values))
	for i := range payloads {
		payloads[i] = &commonpb.Payload{Data: []byte("batch encode")}
	}
	return &commonpb.Payloads{Payloads: payloads}, nil
}

func (dc *boundaryDataConverter) FromPayload(_ *commonpb.Payload, valuePtr any) error {
	dc.fromPayloadCalls++
	transferValue, ok := valuePtr.(*string)
	if !ok {
		return fmt.Errorf("single decode expected *string, got %T", valuePtr)
	}
	*transferValue = "single decode"
	return nil
}

func (dc *boundaryDataConverter) FromPayloads(_ *commonpb.Payloads, valuePtrs ...any) error {
	dc.fromPayloadsCalls++
	for _, valuePtr := range valuePtrs {
		transferValue, ok := valuePtr.(*string)
		if !ok {
			return fmt.Errorf("batch decode expected *string, got %T", valuePtr)
		}
		*transferValue = "batch decode"
	}
	return nil
}

func (dc *boundaryDataConverter) ToString(payload *commonpb.Payload) string {
	dc.toStringCalls++
	dc.toStringPayload = payload
	return "single string"
}

func (dc *boundaryDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	dc.toStringsCalls++
	dc.toStringsPayloads = payloads
	return []string{"batch strings"}
}

type eventDataConverter struct {
	converter.DataConverter
	events *[]string
}

func (dc *eventDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	*dc.events = append(*dc.events, "parent.ToPayload")
	return dc.DataConverter.ToPayload(value)
}

func (dc *eventDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	*dc.events = append(*dc.events, "parent.FromPayload")
	return dc.DataConverter.FromPayload(payload, valuePtr)
}

type eventCodec struct {
	events *[]string
}

func (codec *eventCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	*codec.events = append(*codec.events, "codec.Encode")
	return payloads, nil
}

func (codec *eventCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	*codec.events = append(*codec.events, "codec.Decode")
	return payloads, nil
}

type serializationContextCall struct {
	context converter.SerializationContext
	value   any
}

type serializationContextDataConverter struct {
	converter.DataConverter
	seenContexts *[]converter.SerializationContext
	calls        *[]serializationContextCall
	current      converter.SerializationContext
}

func (dc *serializationContextDataConverter) WithSerializationContext(
	ctx converter.SerializationContext,
) converter.DataConverter {
	*dc.seenContexts = append(*dc.seenContexts, ctx)
	return &serializationContextDataConverter{
		DataConverter: dc.DataConverter,
		seenContexts:  dc.seenContexts,
		calls:         dc.calls,
		current:       ctx,
	}
}

func (dc *serializationContextDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	*dc.calls = append(*dc.calls, serializationContextCall{context: dc.current, value: value})
	return dc.DataConverter.ToPayload(value)
}

type legacyContextKey struct{}

type legacyContextCall struct {
	kind         string
	contextValue any
	value        any
}

type legacyContextDataConverter struct {
	converter.DataConverter
	calls        *[]legacyContextCall
	currentKind  string
	currentValue any
}

func (dc *legacyContextDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	return &legacyContextDataConverter{
		DataConverter: dc.DataConverter,
		calls:         dc.calls,
		currentKind:   "activity",
		currentValue:  ctx.Value(legacyContextKey{}),
	}
}

func (dc *legacyContextDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	return &legacyContextDataConverter{
		DataConverter: dc.DataConverter,
		calls:         dc.calls,
		currentKind:   "workflow",
		currentValue:  ctx.Value(legacyContextKey{}),
	}
}

func (dc *legacyContextDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	*dc.calls = append(*dc.calls, legacyContextCall{
		kind:         dc.currentKind,
		contextValue: dc.currentValue,
		value:        value,
	})
	return dc.DataConverter.ToPayload(value)
}

func TestTransferTypeDataConverterWrapper(t *testing.T) {
	require.Nil(t, wrapTransferTypeDataConverter(nil))

	parent := converter.GetDefaultDataConverter()
	wrapped := wrapTransferTypeDataConverter(parent)
	require.IsType(t, (*transferTypeDataConverter)(nil), wrapped)
	require.Same(t, wrapped, wrapTransferTypeDataConverter(wrapped))
}

func TestTransferTypeDataConverterRoundTrips(t *testing.T) {
	t.Run("string transfer type", func(t *testing.T) {
		dc := wrapTransferTypeDataConverter(converter.GetDefaultDataConverter())
		want := stringTransferValue{Value: "customer-123"}

		payload, err := dc.ToPayload(want)
		require.NoError(t, err)
		require.Equal(t, converter.MetadataEncodingJSON, string(payload.Metadata[converter.MetadataEncoding]))
		require.Equal(t, []byte(`"customer-123"`), payload.Data)

		var got stringTransferValue
		require.NoError(t, dc.FromPayload(payload, &got))
		require.Equal(t, want, got)
	})

	t.Run("protobuf transfer type and existing metadata", func(t *testing.T) {
		parent := converter.NewCompositeDataConverter(
			converter.NewNilPayloadConverter(),
			converter.NewProtoPayloadConverter(),
			converter.NewJSONPayloadConverter(),
		)
		dc := wrapTransferTypeDataConverter(parent)
		want := protoTransferValue{Value: "protobuf-value"}

		payload, err := dc.ToPayload(want)
		require.NoError(t, err)
		require.Equal(t, converter.MetadataEncodingProto, string(payload.Metadata[converter.MetadataEncoding]))
		require.Equal(t, "google.protobuf.StringValue", string(payload.Metadata[converter.MetadataMessageType]))
		require.Len(t, payload.Metadata, 2)
		for key := range payload.Metadata {
			require.NotContains(t, strings.ToLower(key), "transfer", "metadata key %q must come from the parent converter", key)
		}

		parentPayload, err := parent.ToPayload(wrapperspb.String(want.Value))
		require.NoError(t, err)
		require.True(t, proto.Equal(parentPayload, payload), "adapter payload differs from direct protobuf parent payload")

		var got protoTransferValue
		require.NoError(t, dc.FromPayload(payload, &got))
		require.Equal(t, want, got)
	})
}

func TestTransferTypeDataConverterUnmarkedPassThrough(t *testing.T) {
	parent := converter.GetDefaultDataConverter()
	recordingParent := newRecordingDataConverter()
	dc := wrapTransferTypeDataConverter(recordingParent)
	want := plainTransferTestValue{Number: 42}

	directPayload, err := parent.ToPayload(want)
	require.NoError(t, err)
	payload, err := dc.ToPayload(want)
	require.NoError(t, err)
	require.Equal(t, 1, recordingParent.toPayloadCalls)
	require.Equal(t, want, recordingParent.toPayloadValues[0])
	require.Equal(t, directPayload.Metadata, payload.Metadata)
	require.Equal(t, directPayload.Data, payload.Data, "unmarked payload bytes changed")

	var got plainTransferTestValue
	require.NoError(t, dc.FromPayload(payload, &got))
	require.Equal(t, want, got)
	require.Equal(t, 1, recordingParent.fromPayloadCalls)
	require.Same(t, payload, recordingParent.fromPayloadPayload)
	require.Same(t, &got, recordingParent.fromPayloadValuePtr)
}

func TestTransferTypeDataConverterMixedBatch(t *testing.T) {
	parent := newRecordingDataConverter()
	dc := wrapTransferTypeDataConverter(parent)
	rawPayload := &commonpb.Payload{
		Metadata: map[string][]byte{"raw": []byte("metadata")},
		Data:     []byte("raw-data"),
	}
	marked := stringTransferValue{Value: "marked"}
	unmarked := plainTransferTestValue{Number: 7}
	raw := converter.NewRawValue(rawPayload)

	payloads, err := dc.ToPayloads(marked, unmarked, raw)
	require.NoError(t, err)
	require.Equal(t, 0, parent.toPayloadCalls)
	require.Equal(t, 1, parent.toPayloadsCalls)
	require.Len(t, parent.toPayloadsValues, 3)
	require.Equal(t, "marked", parent.toPayloadsValues[0])
	require.Equal(t, unmarked, parent.toPayloadsValues[1])
	require.Equal(t, raw, parent.toPayloadsValues[2])
	require.Len(t, payloads.GetPayloads(), 3)
	require.Same(t, rawPayload, payloads.GetPayloads()[2])

	var markedResult stringTransferValue
	var unmarkedResult plainTransferTestValue
	var rawResult converter.RawValue
	require.NoError(t, dc.FromPayloads(payloads, &markedResult, &unmarkedResult, &rawResult))
	require.Equal(t, 0, parent.fromPayloadCalls)
	require.Equal(t, 1, parent.fromPayloadsCalls)
	require.IsType(t, (*string)(nil), parent.fromPayloadsValuePtr[0])
	require.Same(t, &unmarkedResult, parent.fromPayloadsValuePtr[1])
	require.Same(t, &rawResult, parent.fromPayloadsValuePtr[2])
	require.Equal(t, marked, markedResult)
	require.Equal(t, unmarked, unmarkedResult)
	require.Same(t, rawPayload, rawResult.Payload())
}

func TestTransferTypeDataConverterPreservesParentMethodBoundaries(t *testing.T) {
	t.Run("ToPayload uses only parent ToPayload", func(t *testing.T) {
		parent := &boundaryDataConverter{}
		dc := wrapTransferTypeDataConverter(parent)

		payload, err := dc.ToPayload(stringTransferValue{Value: "application"})
		require.NoError(t, err)
		require.Equal(t, []byte("single encode"), payload.Data)
		require.Equal(t, "application", parent.toPayloadValue)
		require.Equal(t, 1, parent.toPayloadCalls)
		require.Zero(t, parent.toPayloadsCalls)
	})

	t.Run("ToPayloads uses only parent ToPayloads", func(t *testing.T) {
		parent := &boundaryDataConverter{}
		dc := wrapTransferTypeDataConverter(parent)

		payloads, err := dc.ToPayloads(stringTransferValue{Value: "application"})
		require.NoError(t, err)
		require.Equal(t, []byte("batch encode"), payloads.GetPayloads()[0].Data)
		require.Equal(t, []any{"application"}, parent.toPayloadsValues)
		require.Zero(t, parent.toPayloadCalls)
		require.Equal(t, 1, parent.toPayloadsCalls)
	})

	t.Run("FromPayload uses only parent FromPayload", func(t *testing.T) {
		parent := &boundaryDataConverter{}
		dc := wrapTransferTypeDataConverter(parent)
		var got stringTransferValue

		err := dc.FromPayload(&commonpb.Payload{}, &got)
		require.NoError(t, err)
		require.Equal(t, stringTransferValue{Value: "single decode"}, got)
		require.Equal(t, 1, parent.fromPayloadCalls)
		require.Zero(t, parent.fromPayloadsCalls)
	})

	t.Run("FromPayloads uses only parent FromPayloads", func(t *testing.T) {
		parent := &boundaryDataConverter{}
		dc := wrapTransferTypeDataConverter(parent)
		var got stringTransferValue

		err := dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{{}}}, &got)
		require.NoError(t, err)
		require.Equal(t, stringTransferValue{Value: "batch decode"}, got)
		require.Zero(t, parent.fromPayloadCalls)
		require.Equal(t, 1, parent.fromPayloadsCalls)
	})
}

func TestTransferTypeDataConverterMarkerMethodSets(t *testing.T) {
	parent := converter.GetDefaultDataConverter()
	dc := wrapTransferTypeDataConverter(parent)

	t.Run("value receiver marks T and pointer to T", func(t *testing.T) {
		value := stringTransferValue{Value: "value receiver"}
		_, valueMarked := any(value).(converter.TransferTypeConvertible)
		_, pointerMarked := any(&value).(converter.TransferTypeConvertible)
		require.True(t, valueMarked)
		require.True(t, pointerMarked)

		wantPayload, err := parent.ToPayload(value.Value)
		require.NoError(t, err)
		for name, source := range map[string]any{"T": value, "pointer to T": &value} {
			t.Run(name, func(t *testing.T) {
				payload, err := dc.ToPayload(source)
				require.NoError(t, err)
				require.True(t, proto.Equal(wantPayload, payload))
			})
		}
	})

	t.Run("pointer receiver marks only pointer source", func(t *testing.T) {
		value := pointerMarkerValue{Value: "pointer receiver"}
		_, valueMarked := any(value).(converter.TransferTypeConvertible)
		_, pointerMarked := any(&value).(converter.TransferTypeConvertible)
		require.False(t, valueMarked)
		require.True(t, pointerMarked)

		unmarkedPayload, err := dc.ToPayload(value)
		require.NoError(t, err)
		wantUnmarkedPayload, err := parent.ToPayload(value)
		require.NoError(t, err)
		require.True(t, proto.Equal(wantUnmarkedPayload, unmarkedPayload))

		markedPayload, err := dc.ToPayload(&value)
		require.NoError(t, err)
		wantMarkedPayload, err := parent.ToPayload(value.Value)
		require.NoError(t, err)
		require.True(t, proto.Equal(wantMarkedPayload, markedPayload))

		var got pointerMarkerValue
		require.NoError(t, dc.FromPayload(markedPayload, &got))
		require.Equal(t, value, got)
	})
}

func TestTransferTypeDataConverterNilBehavior(t *testing.T) {
	t.Run("untyped nil source delegates", func(t *testing.T) {
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)

		_, err := dc.ToPayload(nil)
		require.NoError(t, err)
		require.Equal(t, 1, parent.toPayloadCalls)
		require.Nil(t, parent.toPayloadValues[0])
	})

	t.Run("typed nil source delegates without invoking marker", func(t *testing.T) {
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		var source *panicValueReceiverMarker

		_, err := dc.ToPayload(source)
		require.NoError(t, err)
		require.Equal(t, 1, parent.toPayloadCalls)
		require.IsType(t, (*panicValueReceiverMarker)(nil), parent.toPayloadValues[0])
	})

	t.Run("nil sources in a batch delegate in one batch call", func(t *testing.T) {
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		var typedNil *panicValueReceiverMarker

		_, err := dc.ToPayloads(nil, typedNil)
		require.NoError(t, err)
		require.Zero(t, parent.toPayloadCalls)
		require.Equal(t, 1, parent.toPayloadsCalls)
		require.Nil(t, parent.toPayloadsValues[0])
		require.IsType(t, (*panicValueReceiverMarker)(nil), parent.toPayloadsValues[1])
	})

	t.Run("nil payload delegates without invoking marker", func(t *testing.T) {
		parentErr := errors.New("nil single payload delegated")
		parent := newRecordingDataConverter()
		parent.fromPayloadFn = func(payload *commonpb.Payload, _ any) error {
			require.Nil(t, payload)
			return parentErr
		}
		dc := wrapTransferTypeDataConverter(parent)
		var destination panicValueReceiverMarker

		err := dc.FromPayload(nil, &destination)
		require.ErrorIs(t, err, parentErr)
		require.Equal(t, 1, parent.fromPayloadCalls)
		require.Same(t, &destination, parent.fromPayloadValuePtr)
	})

	t.Run("nil payload list delegates without invoking marker", func(t *testing.T) {
		parentErr := errors.New("nil batch payload delegated")
		parent := newRecordingDataConverter()
		parent.fromPayloadsFn = func(payloads *commonpb.Payloads, _ ...any) error {
			require.Nil(t, payloads)
			return parentErr
		}
		dc := wrapTransferTypeDataConverter(parent)
		var destination panicValueReceiverMarker

		err := dc.FromPayloads(nil, &destination)
		require.ErrorIs(t, err, parentErr)
		require.Equal(t, 1, parent.fromPayloadsCalls)
		require.Same(t, &destination, parent.fromPayloadsValuePtr[0])
	})

	t.Run("nil payload item delegates without reconstructing", func(t *testing.T) {
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		destination := stringTransferValue{Value: "unchanged"}

		err := dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{nil}}, &destination)
		require.NoError(t, err)
		require.Equal(t, stringTransferValue{Value: "unchanged"}, destination)
		require.Equal(t, 1, parent.fromPayloadsCalls)
		require.Same(t, &destination, parent.fromPayloadsValuePtr[0])
	})

	t.Run("encoded null uses pointer-to-pointer transfer destination", func(t *testing.T) {
		var createdTarget any
		var receivedTarget any
		var transferConverter *testTransferTypeConverter
		transferConverter = &testTransferTypeConverter{
			newTransferTypeFn: func() any {
				var transferValue *string
				createdTarget = &transferValue
				return createdTarget
			},
			toTransferTypeFn: func(any) (any, error) {
				var transferValue *string
				return transferValue, nil
			},
			fromTransferTypeFn: func(value any) (any, error) {
				receivedTarget = value
				return configurableTransferValue{
					Value:             "decoded null",
					TransferConverter: transferConverter,
				}, nil
			},
		}
		dc := wrapTransferTypeDataConverter(converter.GetDefaultDataConverter())
		source := configurableTransferValue{TransferConverter: transferConverter}

		payload, err := dc.ToPayload(source)
		require.NoError(t, err)
		require.Equal(t, converter.MetadataEncodingNil, string(payload.Metadata[converter.MetadataEncoding]))

		destination := configurableTransferValue{TransferConverter: transferConverter}
		require.NoError(t, dc.FromPayload(payload, &destination))
		require.Equal(t, "decoded null", destination.Value)
		require.IsType(t, (**string)(nil), createdTarget)
		require.Same(t, createdTarget, receivedTarget, "FromTransferType did not receive the exact NewTransferType pointer")
		require.Nil(t, *createdTarget.(**string), "encoded null did not decode to a nil transfer pointer")
	})
}

func TestTransferTypeDataConverterDecodeDestinationDiscovery(t *testing.T) {
	parent := converter.GetDefaultDataConverter()
	dc := wrapTransferTypeDataConverter(parent)
	payload, err := parent.ToPayload("decoded")
	require.NoError(t, err)

	t.Run("assigns into T destination", func(t *testing.T) {
		var destination stringTransferValue
		require.NoError(t, dc.FromPayload(payload, &destination))
		require.Equal(t, stringTransferValue{Value: "decoded"}, destination)
	})

	t.Run("discovers marker and assigns into pointer to T destination", func(t *testing.T) {
		var destination *pointerDestinationValue
		_, suppliedDestinationMarked := any(&destination).(converter.TransferTypeConvertible)
		require.False(t, suppliedDestinationMarked, "pointer-to-pointer unexpectedly implements the marker")

		require.NoError(t, dc.FromPayload(payload, &destination))
		require.Equal(t, &pointerDestinationValue{Value: "decoded"}, destination)
	})
}

func TestTransferTypeDataConverterInvalidDestinationsDelegate(t *testing.T) {
	payload := &commonpb.Payload{Data: []byte("payload")}
	parentErr := errors.New("parent destination validation")
	var typedNil *panicValueReceiverMarker

	tests := []struct {
		name        string
		destination any
	}{
		{name: "nil interface", destination: nil},
		{name: "non-pointer marked value", destination: panicValueReceiverMarker{}},
		{name: "typed nil marked pointer", destination: typedNil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := newRecordingDataConverter()
			parent.fromPayloadFn = func(_ *commonpb.Payload, _ any) error {
				return parentErr
			}
			dc := wrapTransferTypeDataConverter(parent)

			err := dc.FromPayload(payload, test.destination)
			require.ErrorIs(t, err, parentErr)
			require.Equal(t, 1, parent.fromPayloadCalls)
			require.Equal(t, test.destination, parent.fromPayloadValuePtr)
		})
	}
}

func TestTransferTypeDataConverterTransferConverterValidation(t *testing.T) {
	var typedNilConverter *testTransferTypeConverter
	converters := []struct {
		name      string
		converter converter.TransferTypeConverter
	}{
		{name: "nil", converter: nil},
		{name: "typed nil", converter: typedNilConverter},
	}

	for _, test := range converters {
		t.Run("encode with "+test.name+" converter", func(t *testing.T) {
			parent := newRecordingDataConverter()
			dc := wrapTransferTypeDataConverter(parent)
			source := configurableTransferValue{TransferConverter: test.converter}

			_, err := dc.ToPayload(source)
			require.Error(t, err)
			require.Contains(t, err.Error(), "transfer type converter")
			require.Contains(t, err.Error(), "configurableTransferValue")
			require.Contains(t, err.Error(), "nil")
			require.Zero(t, parent.toPayloadCalls)
		})

		t.Run("decode with "+test.name+" converter", func(t *testing.T) {
			parent := newRecordingDataConverter()
			dc := wrapTransferTypeDataConverter(parent)
			destination := configurableTransferValue{TransferConverter: test.converter}

			err := dc.FromPayload(&commonpb.Payload{}, &destination)
			require.Error(t, err)
			require.Contains(t, err.Error(), "transfer type converter")
			require.Contains(t, err.Error(), "destination")
			require.Contains(t, err.Error(), "nil")
			require.Zero(t, parent.fromPayloadCalls)
		})
	}

	t.Run("NewTransferType must return a non-nil pointer", func(t *testing.T) {
		var typedNilTarget *string
		tests := []struct {
			name   string
			result any
		}{
			{name: "nil", result: nil},
			{name: "typed nil pointer", result: typedNilTarget},
			{name: "non-pointer", result: "not a pointer"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				parent := newRecordingDataConverter()
				dc := wrapTransferTypeDataConverter(parent)
				transferConverter := &testTransferTypeConverter{
					newTransferTypeFn: func() any { return test.result },
				}
				destination := configurableTransferValue{TransferConverter: transferConverter}

				err := dc.FromPayload(&commonpb.Payload{}, &destination)
				require.Error(t, err)
				require.Contains(t, err.Error(), "NewTransferType")
				require.Contains(t, err.Error(), "destination")
				require.Contains(t, err.Error(), "non-nil pointer")
				require.Zero(t, parent.fromPayloadCalls)
			})
		}
	})
}

func TestTransferTypeDataConverterErrorPropagation(t *testing.T) {
	t.Run("ToTransferType error", func(t *testing.T) {
		hookErr := errors.New("to-transfer hook failed")
		transferConverter := &testTransferTypeConverter{
			toTransferTypeFn: func(any) (any, error) { return nil, hookErr },
		}
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)

		_, err := dc.ToPayload(configurableTransferValue{TransferConverter: transferConverter})
		require.ErrorIs(t, err, hookErr)
		require.Contains(t, err.Error(), "convert")
		require.Contains(t, err.Error(), "configurableTransferValue")
		require.Zero(t, parent.toPayloadCalls)
	})

	t.Run("parent ToPayload error after transfer", func(t *testing.T) {
		parentErr := errors.New("parent single encode failed")
		parent := newRecordingDataConverter()
		parent.toPayloadFn = func(any) (*commonpb.Payload, error) { return nil, parentErr }
		dc := wrapTransferTypeDataConverter(parent)

		_, err := dc.ToPayload(stringTransferValue{Value: "value"})
		require.ErrorIs(t, err, parentErr)
		require.Contains(t, err.Error(), "encode transfer value")
		require.Contains(t, err.Error(), "stringTransferValue")
		require.Equal(t, 1, parent.toPayloadCalls)
	})

	t.Run("FromTransferType error", func(t *testing.T) {
		hookErr := errors.New("from-transfer hook failed")
		transferConverter := &testTransferTypeConverter{
			newTransferTypeFn:  func() any { return new(string) },
			fromTransferTypeFn: func(any) (any, error) { return nil, hookErr },
		}
		parent := newRecordingDataConverter()
		payload, err := parent.DataConverter.ToPayload("value")
		require.NoError(t, err)
		dc := wrapTransferTypeDataConverter(parent)
		destination := configurableTransferValue{TransferConverter: transferConverter}

		err = dc.FromPayload(payload, &destination)
		require.ErrorIs(t, err, hookErr)
		require.Contains(t, err.Error(), "convert transfer value")
		require.Contains(t, err.Error(), "configurableTransferValue")
		require.Equal(t, 1, parent.fromPayloadCalls)
	})

	t.Run("parent FromPayload error before hook", func(t *testing.T) {
		parentErr := errors.New("parent single decode failed")
		parent := newRecordingDataConverter()
		parent.fromPayloadFn = func(*commonpb.Payload, any) error { return parentErr }
		dc := wrapTransferTypeDataConverter(parent)
		var destination stringTransferValue

		err := dc.FromPayload(&commonpb.Payload{}, &destination)
		require.ErrorIs(t, err, parentErr)
		require.Contains(t, err.Error(), "decode transfer value")
		require.Contains(t, err.Error(), "stringTransferValue")
		require.Equal(t, 1, parent.fromPayloadCalls)
	})

	t.Run("parent ToPayloads error", func(t *testing.T) {
		parentErr := errors.New("parent batch encode failed")
		parent := newRecordingDataConverter()
		parent.toPayloadsFn = func(...any) (*commonpb.Payloads, error) { return nil, parentErr }
		dc := wrapTransferTypeDataConverter(parent)

		_, err := dc.ToPayloads(stringTransferValue{Value: "value"})
		require.ErrorIs(t, err, parentErr)
		require.Zero(t, parent.toPayloadCalls)
		require.Equal(t, 1, parent.toPayloadsCalls)
	})

	t.Run("parent FromPayloads error", func(t *testing.T) {
		parentErr := errors.New("parent batch decode failed")
		parent := newRecordingDataConverter()
		parent.fromPayloadsFn = func(*commonpb.Payloads, ...any) error { return parentErr }
		dc := wrapTransferTypeDataConverter(parent)
		var destination stringTransferValue

		err := dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{{}}}, &destination)
		require.ErrorIs(t, err, parentErr)
		require.Zero(t, parent.fromPayloadCalls)
		require.Equal(t, 1, parent.fromPayloadsCalls)
	})
}

func TestTransferTypeDataConverterResultValidation(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload("decoded")
	require.NoError(t, err)

	t.Run("wrong result type", func(t *testing.T) {
		transferConverter := &testTransferTypeConverter{
			newTransferTypeFn:  func() any { return new(string) },
			fromTransferTypeFn: func(any) (any, error) { return 123, nil },
		}
		destination := configurableTransferValue{
			Value:             "unchanged",
			TransferConverter: transferConverter,
		}
		dc := wrapTransferTypeDataConverter(converter.GetDefaultDataConverter())

		err := dc.FromPayload(payload, &destination)
		require.Error(t, err)
		require.Contains(t, err.Error(), "returned int")
		require.Contains(t, err.Error(), "cannot assign")
		require.Contains(t, err.Error(), "configurableTransferValue")
		require.Equal(t, "unchanged", destination.Value)
	})

	t.Run("nil result rejected for non-nilable destination", func(t *testing.T) {
		transferConverter := &testTransferTypeConverter{
			newTransferTypeFn:  func() any { return new(string) },
			fromTransferTypeFn: func(any) (any, error) { return nil, nil },
		}
		destination := configurableTransferValue{
			Value:             "unchanged",
			TransferConverter: transferConverter,
		}
		dc := wrapTransferTypeDataConverter(converter.GetDefaultDataConverter())

		err := dc.FromPayload(payload, &destination)
		require.Error(t, err)
		require.Contains(t, err.Error(), "returned nil")
		require.Contains(t, err.Error(), "cannot assign")
		require.Contains(t, err.Error(), "configurableTransferValue")
		require.Equal(t, "unchanged", destination.Value)
	})

	t.Run("nil result accepted for nilable destination", func(t *testing.T) {
		destination := nilableTransferMap{"existing": "value"}
		dc := wrapTransferTypeDataConverter(converter.GetDefaultDataConverter())

		require.NoError(t, dc.FromPayload(payload, &destination))
		require.Nil(t, destination)
	})
}

func TestTransferTypeDataConverterBatchErrorsAndNonAtomicBehavior(t *testing.T) {
	t.Run("encode hook error includes value index and skips parent", func(t *testing.T) {
		hookErr := errors.New("second encode hook failed")
		goodConverter := &testTransferTypeConverter{
			toTransferTypeFn: func(any) (any, error) { return "first", nil },
		}
		badConverter := &testTransferTypeConverter{
			toTransferTypeFn: func(any) (any, error) { return nil, hookErr },
		}
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)

		_, err := dc.ToPayloads(
			configurableTransferValue{TransferConverter: goodConverter},
			configurableTransferValue{TransferConverter: badConverter},
		)
		require.ErrorIs(t, err, hookErr)
		require.Contains(t, err.Error(), "values[1]")
		require.Zero(t, parent.toPayloadsCalls)
	})

	t.Run("prepare error includes payload index and skips parent", func(t *testing.T) {
		goodConverter := &testTransferTypeConverter{
			newTransferTypeFn: func() any { return new(string) },
		}
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		first := configurableTransferValue{TransferConverter: goodConverter}
		second := configurableTransferValue{}

		err := dc.FromPayloads(
			&commonpb.Payloads{Payloads: []*commonpb.Payload{{}, {}}},
			&first,
			&second,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "payload item 1")
		require.Contains(t, err.Error(), "converter")
		require.Zero(t, parent.fromPayloadsCalls)
	})

	t.Run("later hook error leaves earlier parent and hook assignments visible", func(t *testing.T) {
		hookErr := errors.New("second decode hook failed")
		var firstConverter *testTransferTypeConverter
		firstConverter = &testTransferTypeConverter{
			newTransferTypeFn: func() any { return new(string) },
			fromTransferTypeFn: func(value any) (any, error) {
				return configurableTransferValue{
					Value:             *value.(*string),
					TransferConverter: firstConverter,
				}, nil
			},
		}
		secondConverter := &testTransferTypeConverter{
			newTransferTypeFn:  func() any { return new(string) },
			fromTransferTypeFn: func(any) (any, error) { return nil, hookErr },
		}
		defaultConverter := converter.GetDefaultDataConverter()
		payloads, err := defaultConverter.ToPayloads("first", "parent populated", "second")
		require.NoError(t, err)
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		first := configurableTransferValue{Value: "original first", TransferConverter: firstConverter}
		var unmarked string
		second := configurableTransferValue{Value: "original second", TransferConverter: secondConverter}

		err = dc.FromPayloads(payloads, &first, &unmarked, &second)
		require.ErrorIs(t, err, hookErr)
		require.Contains(t, err.Error(), "payload item 2")
		require.Equal(t, "first", first.Value, "earlier transfer hook assignment was rolled back")
		require.Equal(t, "parent populated", unmarked, "parent assignment was rolled back")
		require.Equal(t, "original second", second.Value)
		require.Zero(t, parent.fromPayloadCalls)
		require.Equal(t, 1, parent.fromPayloadsCalls)
	})
}

func TestTransferTypeDataConverterBatchCountMismatch(t *testing.T) {
	defaultConverter := converter.GetDefaultDataConverter()

	t.Run("more payloads than destinations", func(t *testing.T) {
		payloads, err := defaultConverter.ToPayloads("first", "ignored")
		require.NoError(t, err)
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		var destination stringTransferValue

		require.NoError(t, dc.FromPayloads(payloads, &destination))
		require.Equal(t, stringTransferValue{Value: "first"}, destination)
		require.Equal(t, 1, parent.fromPayloadsCalls)
		require.Len(t, parent.fromPayloadsValuePtr, 1)
	})

	t.Run("fewer payloads than destinations", func(t *testing.T) {
		payloads, err := defaultConverter.ToPayloads("first")
		require.NoError(t, err)
		newTransferTypeCalls := 0
		extraConverter := &testTransferTypeConverter{
			newTransferTypeFn: func() any {
				newTransferTypeCalls++
				return new(string)
			},
		}
		parent := newRecordingDataConverter()
		dc := wrapTransferTypeDataConverter(parent)
		var first stringTransferValue
		extra := configurableTransferValue{Value: "unchanged", TransferConverter: extraConverter}

		require.NoError(t, dc.FromPayloads(payloads, &first, &extra))
		require.Equal(t, stringTransferValue{Value: "first"}, first)
		require.Equal(t, "unchanged", extra.Value)
		require.Zero(t, newTransferTypeCalls, "adapter prepared a destination with no matching payload")
		require.Equal(t, 1, parent.fromPayloadsCalls)
		require.Len(t, parent.fromPayloadsValuePtr, 2, "adapter changed the destination count seen by the parent")
	})
}

func TestTransferTypeDataConverterTopLevelOnly(t *testing.T) {
	parent := converter.GetDefaultDataConverter()
	dc := wrapTransferTypeDataConverter(parent)

	tests := []struct {
		name  string
		value func(*int) any
	}{
		{
			name: "slice elements",
			value: func(calls *int) any {
				return []topLevelOnlyValue{{Value: "nested", calls: calls}}
			},
		},
		{
			name: "map values",
			value: func(calls *int) any {
				return map[string]topLevelOnlyValue{"key": {Value: "nested", calls: calls}}
			},
		},
		{
			name: "struct fields",
			value: func(calls *int) any {
				return topLevelOnlyContainer{Nested: topLevelOnlyValue{Value: "nested", calls: calls}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			value := test.value(&calls)
			want, err := parent.ToPayload(value)
			require.NoError(t, err)

			got, err := dc.ToPayload(value)
			require.NoError(t, err)
			require.True(t, proto.Equal(want, got), "nested marker changed parent serialization")
			require.Zero(t, calls, "nested transfer hook was invoked")
		})
	}

	t.Run("top-level value still invokes hook", func(t *testing.T) {
		calls := 0
		_, err := dc.ToPayload(topLevelOnlyValue{Value: "top-level", calls: &calls})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
	})
}

func TestTransferTypeDataConverterCodecOrder(t *testing.T) {
	events := []string{}
	baseParent := &eventDataConverter{
		DataConverter: converter.GetDefaultDataConverter(),
		events:        &events,
	}
	codecParent := converter.NewCodecDataConverter(baseParent, &eventCodec{events: &events})
	dc := wrapTransferTypeDataConverter(codecParent)
	var transferConverter *testTransferTypeConverter
	transferConverter = &testTransferTypeConverter{
		newTransferTypeFn: func() any { return new(string) },
		toTransferTypeFn: func(any) (any, error) {
			events = append(events, "hook.ToTransferType")
			return "wire value", nil
		},
		fromTransferTypeFn: func(value any) (any, error) {
			events = append(events, "hook.FromTransferType")
			return configurableTransferValue{
				Value:             *value.(*string),
				TransferConverter: transferConverter,
			}, nil
		},
	}
	source := configurableTransferValue{TransferConverter: transferConverter}

	payload, err := dc.ToPayload(source)
	require.NoError(t, err)
	require.Equal(t, []string{
		"hook.ToTransferType",
		"parent.ToPayload",
		"codec.Encode",
	}, events)

	events = events[:0]
	destination := configurableTransferValue{TransferConverter: transferConverter}
	require.NoError(t, dc.FromPayload(payload, &destination))
	require.Equal(t, "wire value", destination.Value)
	require.Equal(t, []string{
		"codec.Decode",
		"parent.FromPayload",
		"hook.FromTransferType",
	}, events)
}

func TestTransferTypeDataConverterSerializationContextReachesParent(t *testing.T) {
	tests := []struct {
		name    string
		context converter.SerializationContext
	}{
		{
			name: "workflow",
			context: converter.WorkflowSerializationContext{
				Namespace:  "namespace",
				WorkflowID: "workflow-id",
			},
		},
		{
			name: "activity",
			context: converter.ActivitySerializationContext{
				Namespace:    "namespace",
				WorkflowID:   "workflow-id",
				WorkflowType: "workflow-type",
				ActivityType: "activity-type",
				TaskQueue:    "task-queue",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seenContexts := []converter.SerializationContext{}
			calls := []serializationContextCall{}
			parent := &serializationContextDataConverter{
				DataConverter: converter.GetDefaultDataConverter(),
				seenContexts:  &seenContexts,
				calls:         &calls,
			}
			wrapped := wrapTransferTypeDataConverter(parent)

			contextual := converter.WithDataConverterSerializationContext(wrapped, test.context)
			require.IsType(t, (*transferTypeDataConverter)(nil), contextual)
			_, err := contextual.ToPayload(stringTransferValue{Value: "transferred"})
			require.NoError(t, err)

			require.Equal(t, []converter.SerializationContext{test.context}, seenContexts)
			require.Equal(t, []serializationContextCall{{
				context: test.context,
				value:   "transferred",
			}}, calls)
		})
	}
}

func TestTransferTypeDataConverterLegacyContextAwareForwarding(t *testing.T) {
	calls := []legacyContextCall{}
	parent := &legacyContextDataConverter{
		DataConverter: converter.GetDefaultDataConverter(),
		calls:         &calls,
	}
	wrapped := wrapTransferTypeDataConverter(parent)

	activityContext := context.WithValue(t.Context(), legacyContextKey{}, "activity value")
	activityConverter := WithContext(activityContext, wrapped)
	require.IsType(t, (*transferTypeDataConverter)(nil), activityConverter)
	_, err := activityConverter.ToPayload(stringTransferValue{Value: "activity transfer"})
	require.NoError(t, err)

	workflowContext := WithValue(Background(), legacyContextKey{}, "workflow value")
	workflowConverter := WithWorkflowContext(workflowContext, wrapped)
	require.IsType(t, (*transferTypeDataConverter)(nil), workflowConverter)
	_, err = workflowConverter.ToPayload(stringTransferValue{Value: "workflow transfer"})
	require.NoError(t, err)

	require.Equal(t, []legacyContextCall{
		{kind: "activity", contextValue: "activity value", value: "activity transfer"},
		{kind: "workflow", contextValue: "workflow value", value: "workflow transfer"},
	}, calls)
}

func TestTransferTypeDataConverterStringDelegation(t *testing.T) {
	parent := &boundaryDataConverter{}
	dc := wrapTransferTypeDataConverter(parent)
	payload := &commonpb.Payload{Data: []byte("single")}
	payloads := &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("batch")}}}

	require.Equal(t, "single string", dc.ToString(payload))
	require.Equal(t, 1, parent.toStringCalls)
	require.Same(t, payload, parent.toStringPayload)

	require.Equal(t, []string{"batch strings"}, dc.ToStrings(payloads))
	require.Equal(t, 1, parent.toStringsCalls)
	require.Same(t, payloads, parent.toStringsPayloads)
	require.Equal(t, 1, parent.toStringCalls, "ToStrings was implemented as repeated ToString calls")
	require.Zero(t, parent.toPayloadCalls)
	require.Zero(t, parent.fromPayloadCalls)
}
