package converter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

type transferTypeTestValue struct{ Value string }

type transferTypeTestValueConverter struct{}

func (transferTypeTestValueConverter) NewTransferType() any { return new(string) }

func (transferTypeTestValueConverter) ToTransferType(value any) (any, error) {
	return value.(*transferTypeTestValue).Value, nil
}

func (transferTypeTestValueConverter) FromTransferType(value any) (any, error) {
	return &transferTypeTestValue{Value: *value.(*string)}, nil
}

func (*transferTypeTestValue) TransferTypeConverter() TransferTypeConverter {
	return transferTypeTestValueConverter{}
}

func TestTransferTypeDataConverter(t *testing.T) {
	dc := GetDefaultDataConverter()
	value := &transferTypeTestValue{Value: "value"}

	payload, err := dc.ToPayload(value)
	require.NoError(t, err)
	require.Equal(t, MetadataEncodingJSON, string(payload.Metadata[MetadataEncoding]))
	require.JSONEq(t, `"value"`, string(payload.Data))

	var decoded transferTypeTestValue
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, *value, decoded)
}

func TestTransferTypeDataConverter_PointerDestination(t *testing.T) {
	dc := GetDefaultDataConverter()
	payload, err := dc.ToPayload(&transferTypeTestValue{Value: "value"})
	require.NoError(t, err)

	var decoded *transferTypeTestValue
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.NotNil(t, decoded)
	require.Equal(t, "value", decoded.Value)
}

type protoTransferTypeTestValue struct{ WorkflowID string }

type protoTransferTypeTestValueConverter struct{}

func (protoTransferTypeTestValueConverter) NewTransferType() any {
	return new(commonpb.WorkflowExecution)
}

func (protoTransferTypeTestValueConverter) ToTransferType(value any) (any, error) {
	return &commonpb.WorkflowExecution{WorkflowId: value.(*protoTransferTypeTestValue).WorkflowID}, nil
}

func (protoTransferTypeTestValueConverter) FromTransferType(value any) (any, error) {
	return &protoTransferTypeTestValue{WorkflowID: value.(*commonpb.WorkflowExecution).WorkflowId}, nil
}

func (*protoTransferTypeTestValue) TransferTypeConverter() TransferTypeConverter {
	return protoTransferTypeTestValueConverter{}
}

func TestTransferTypeDataConverter_ProtoTransferType(t *testing.T) {
	dc := GetDefaultDataConverter()
	payload, err := dc.ToPayload(&protoTransferTypeTestValue{WorkflowID: "workflow-id"})
	require.NoError(t, err)
	require.Equal(t, MetadataEncodingProtoJSON, string(payload.Metadata[MetadataEncoding]))

	var decoded protoTransferTypeTestValue
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, "workflow-id", decoded.WorkflowID)
}

func TestTransferTypeDataConverter_ContextAwareParent(t *testing.T) {
	dc := WrapDataConverter(&transferTypeContextDataConverter{})
	dc = WithDataConverterSerializationContext(dc, WorkflowSerializationContext{WorkflowID: "workflow-id"})

	payload, err := dc.ToPayload(&transferTypeTestValue{Value: "value"})
	require.NoError(t, err)
	require.Equal(t, "workflow-id:value", string(payload.Data))

	var decoded transferTypeTestValue
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, "value", decoded.Value)
}

func TestTransferTypeDataConverter_MixedPayloadsAndRawValue(t *testing.T) {
	dc := GetDefaultDataConverter()
	rawPayload, err := dc.ToPayload("raw")
	require.NoError(t, err)

	payloads, err := dc.ToPayloads(&transferTypeTestValue{Value: "transfer"}, "plain", NewRawValue(rawPayload))
	require.NoError(t, err)

	var transfer transferTypeTestValue
	var plain string
	var raw RawValue
	require.NoError(t, dc.FromPayloads(payloads, &transfer, &plain, &raw))
	require.Equal(t, "transfer", transfer.Value)
	require.Equal(t, "plain", plain)
	require.Same(t, rawPayload, raw.Payload())
}

func TestTransferTypeDataConverter_ConversionErrors(t *testing.T) {
	dc := GetDefaultDataConverter()
	_, err := dc.ToPayload(&failingTransferTypeValue{})
	require.ErrorContains(t, err, "to transfer")

	payload, err := dc.ToPayload("value")
	require.NoError(t, err)
	var decoded failingTransferTypeValue
	err = dc.FromPayload(payload, &decoded)
	require.ErrorContains(t, err, "from transfer")
}

func TestTransferTypeDataConverter_InvalidConverterResults(t *testing.T) {
	dc := GetDefaultDataConverter()
	payload, err := dc.ToPayload("value")
	require.NoError(t, err)

	t.Run("nil converter", func(t *testing.T) {
		var value nilConverterTransferTypeValue
		err := dc.FromPayload(payload, &value)
		require.ErrorContains(t, err, "transfer type converter is nil")
	})
	t.Run("invalid decode target", func(t *testing.T) {
		var value invalidTargetTransferTypeValue
		err := dc.FromPayload(payload, &value)
		require.ErrorContains(t, err, "invalid decode target")
	})
	t.Run("typed nil decode target", func(t *testing.T) {
		var value typedNilTargetTransferTypeValue
		err := dc.FromPayload(payload, &value)
		require.ErrorContains(t, err, "invalid decode target")
	})
	t.Run("wrong converted type", func(t *testing.T) {
		var value wrongResultTransferTypeValue
		err := dc.FromPayload(payload, &value)
		require.ErrorContains(t, err, "cannot assign")
	})
	t.Run("nil converted value", func(t *testing.T) {
		var value nilResultTransferTypeValue
		err := dc.FromPayload(payload, &value)
		require.ErrorContains(t, err, "returned nil")
	})
}

func TestTransferTypeDataConverter_PassThroughAndAssignment(t *testing.T) {
	dc := GetDefaultDataConverter()

	t.Run("nil payload", func(t *testing.T) {
		value := transferTypeTestValue{Value: "unchanged"}
		require.NoError(t, dc.FromPayload(nil, &value))
		require.Equal(t, "unchanged", value.Value)
	})
	t.Run("direct converted assignment", func(t *testing.T) {
		payload, err := dc.ToPayload("value")
		require.NoError(t, err)
		var value directResultTransferTypeValue
		require.NoError(t, dc.FromPayload(payload, &value))
		require.Equal(t, "value", value.Value)
	})
}

func TestTransferTypeDataConverter_BatchConversionError(t *testing.T) {
	_, err := GetDefaultDataConverter().ToPayloads("plain", &failingTransferTypeValue{})
	require.ErrorContains(t, err, "values[1]")
	require.ErrorContains(t, err, "to transfer")
}

func TestTransferTypeDataConverter_ParentErrorsAndPayloadLists(t *testing.T) {
	t.Run("parent errors", func(t *testing.T) {
		dc := WrapDataConverter(&failingTransferParentDataConverter{DataConverter: GetDefaultDataConverter()})
		_, err := dc.ToPayloads(&transferTypeTestValue{Value: "value"})
		require.ErrorContains(t, err, "parent encode")
		err = dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{{}}}, &transferTypeTestValue{})
		require.ErrorContains(t, err, "parent decode")
	})
	t.Run("nil payloads", func(t *testing.T) {
		value := transferTypeTestValue{Value: "unchanged"}
		require.NoError(t, GetDefaultDataConverter().FromPayloads(nil, &value))
		require.Equal(t, "unchanged", value.Value)
	})
	t.Run("nil payload item", func(t *testing.T) {
		value := transferTypeTestValue{Value: "unchanged"}
		require.NoError(t, GetDefaultDataConverter().FromPayloads(
			&commonpb.Payloads{Payloads: []*commonpb.Payload{nil}}, &value))
		require.Equal(t, "unchanged", value.Value)
	})
	t.Run("mismatched payload and destination counts", func(t *testing.T) {
		dc := GetDefaultDataConverter()
		payloads, err := dc.ToPayloads("transfer", "ignored")
		require.NoError(t, err)
		var value transferTypeTestValue
		require.NoError(t, dc.FromPayloads(payloads, &value))
		require.Equal(t, "transfer", value.Value)

		payloads, err = dc.ToPayloads("transfer")
		require.NoError(t, err)
		var extra string
		require.NoError(t, dc.FromPayloads(payloads, &value, &extra))
		require.Equal(t, "transfer", value.Value)
		require.Empty(t, extra)
	})
}

type failingTransferParentDataConverter struct{ DataConverter }

func (*failingTransferParentDataConverter) ToPayloads(...interface{}) (*commonpb.Payloads, error) {
	return nil, errors.New("parent encode")
}

func (*failingTransferParentDataConverter) FromPayloads(*commonpb.Payloads, ...interface{}) error {
	return errors.New("parent decode")
}

func TestTransferTypeDataConverter_CodecAndDelegation(t *testing.T) {
	dc := WrapDataConverter(NewCodecDataConverter(
		GetDefaultDataConverter(),
		NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true}),
	))
	require.Same(t, dc, WrapDataConverter(dc))

	payload, err := dc.ToPayload(&transferTypeTestValue{Value: "value"})
	require.NoError(t, err)
	require.Equal(t, "binary/zlib", string(payload.Metadata[MetadataEncoding]))
	require.NotEmpty(t, dc.ToString(payload))

	var decoded transferTypeTestValue
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, "value", decoded.Value)
}

type failingTransferTypeValue struct{}

func (failingTransferTypeValue) TransferTypeConverter() TransferTypeConverter {
	return failingTransferTypeConverter{}
}

type failingTransferTypeConverter struct{}

func (failingTransferTypeConverter) NewTransferType() any { return new(string) }

func (failingTransferTypeConverter) ToTransferType(any) (any, error) {
	return nil, errors.New("to transfer")
}

func (failingTransferTypeConverter) FromTransferType(any) (any, error) {
	return nil, errors.New("from transfer")
}

type nilConverterTransferTypeValue struct{}

func (*nilConverterTransferTypeValue) TransferTypeConverter() TransferTypeConverter { return nil }

type invalidTargetTransferTypeValue struct{}

func (*invalidTargetTransferTypeValue) TransferTypeConverter() TransferTypeConverter {
	return invalidTargetTransferTypeConverter{}
}

type invalidTargetTransferTypeConverter struct{}

func (invalidTargetTransferTypeConverter) NewTransferType() any              { return "not-a-pointer" }
func (invalidTargetTransferTypeConverter) ToTransferType(any) (any, error)   { return nil, nil }
func (invalidTargetTransferTypeConverter) FromTransferType(any) (any, error) { return nil, nil }

type typedNilTargetTransferTypeValue struct{}

func (*typedNilTargetTransferTypeValue) TransferTypeConverter() TransferTypeConverter {
	return typedNilTargetTransferTypeConverter{}
}

type typedNilTargetTransferTypeConverter struct{}

func (typedNilTargetTransferTypeConverter) NewTransferType() any              { return (*string)(nil) }
func (typedNilTargetTransferTypeConverter) ToTransferType(any) (any, error)   { return nil, nil }
func (typedNilTargetTransferTypeConverter) FromTransferType(any) (any, error) { return nil, nil }

type wrongResultTransferTypeValue struct{}

func (*wrongResultTransferTypeValue) TransferTypeConverter() TransferTypeConverter {
	return wrongResultTransferTypeConverter{}
}

type wrongResultTransferTypeConverter struct{}

func (wrongResultTransferTypeConverter) NewTransferType() any { return new(string) }
func (wrongResultTransferTypeConverter) ToTransferType(any) (any, error) {
	return nil, nil
}
func (wrongResultTransferTypeConverter) FromTransferType(any) (any, error) { return 1, nil }

type nilResultTransferTypeValue struct{}

func (*nilResultTransferTypeValue) TransferTypeConverter() TransferTypeConverter {
	return nilResultTransferTypeConverter{}
}

type nilResultTransferTypeConverter struct{}

func (nilResultTransferTypeConverter) NewTransferType() any              { return new(string) }
func (nilResultTransferTypeConverter) ToTransferType(any) (any, error)   { return nil, nil }
func (nilResultTransferTypeConverter) FromTransferType(any) (any, error) { return nil, nil }

type directResultTransferTypeValue struct{ Value string }

func (directResultTransferTypeValue) TransferTypeConverter() TransferTypeConverter {
	return directResultTransferTypeConverter{}
}

type directResultTransferTypeConverter struct{}

func (directResultTransferTypeConverter) NewTransferType() any { return new(string) }
func (directResultTransferTypeConverter) ToTransferType(value any) (any, error) {
	return value.(directResultTransferTypeValue).Value, nil
}
func (directResultTransferTypeConverter) FromTransferType(value any) (any, error) {
	return directResultTransferTypeValue{Value: *value.(*string)}, nil
}

type transferTypeContextDataConverter struct{ workflowID string }

func (dc *transferTypeContextDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	stringValue, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("expected string, got %T", value)
	}
	return &commonpb.Payload{
		Metadata: map[string][]byte{MetadataEncoding: []byte("test/transfer")},
		Data:     []byte(dc.workflowID + ":" + stringValue),
	}, nil
}

func (dc *transferTypeContextDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	stringPtr, ok := valuePtr.(*string)
	if !ok {
		return fmt.Errorf("expected *string, got %T", valuePtr)
	}
	*stringPtr = string(payload.Data[len(dc.workflowID)+1:])
	return nil
}

func (dc *transferTypeContextDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	payloads := make([]*commonpb.Payload, len(values))
	for i, value := range values {
		payload, err := dc.ToPayload(value)
		if err != nil {
			return nil, err
		}
		payloads[i] = payload
	}
	return &commonpb.Payloads{Payloads: payloads}, nil
}

func (dc *transferTypeContextDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	for i, payload := range payloads.Payloads {
		if i >= len(valuePtrs) {
			break
		}
		if err := dc.FromPayload(payload, valuePtrs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (*transferTypeContextDataConverter) ToString(payload *commonpb.Payload) string {
	return string(payload.Data)
}

func (dc *transferTypeContextDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	strings := make([]string, len(payloads.Payloads))
	for i, payload := range payloads.Payloads {
		strings[i] = dc.ToString(payload)
	}
	return strings
}

func (dc *transferTypeContextDataConverter) WithSerializationContext(ctx SerializationContext) DataConverter {
	workflowContext, ok := ctx.(WorkflowSerializationContext)
	if !ok {
		return dc
	}
	return &transferTypeContextDataConverter{workflowID: workflowContext.WorkflowID}
}
