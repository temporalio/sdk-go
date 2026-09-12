package internal

import (
	"fmt"
	"reflect"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- USER API -----------------------------------------------------------

// TransferTypeConvertible provides a transfer type converter for values of
// this type. The SDK calls TransferTypeConverter before serialization and
// after deserialization.
//
// NOTE: Experimental.
type TransferTypeConvertible interface {
	TransferTypeConverter() TransferTypeConverter
}

// TransferTypeConverter converts application values to serializable transfer
// values and back. Implement it using [NewTransferTypeConverter].
//
// NOTE: Experimental.
type TransferTypeConverter interface {
	NewTransferValuePtr() any
	ToTransferValue(value any) (any, error)
	FromTransferValue(transferValue any, valuePtr any) error
	transferTypeConverter()
}

// NewTransferTypeConverter builds a [TransferTypeConverter] that can map
// something of type Value into a serializable "transfer value", and back.
//
// NOTE: Experimental.
func NewTransferTypeConverter[Value, TransferValue any](
	toTransferValue func(Value) (TransferValue, error),
	fromTransferValue func(TransferValue, *Value) error,
) TransferTypeConverter {
	return &transferTypeConverter[Value, TransferValue]{
		toTransferValue:   toTransferValue,
		fromTransferValue: fromTransferValue,
	}
}

type transferTypeConverter[Value, TransferValue any] struct {
	toTransferValue   func(Value) (TransferValue, error)
	fromTransferValue func(TransferValue, *Value) error
}

func (*transferTypeConverter[Value, TransferValue]) transferTypeConverter() {}

func (*transferTypeConverter[Value, TransferValue]) NewTransferValuePtr() any {
	transferType := reflect.TypeFor[TransferValue]()
	if transferType.Kind() != reflect.Pointer {
		return new(TransferValue)
	}

	transferValue := reflect.New(transferType.Elem())
	if transferValue.Type() != transferType {
		transferValue = transferValue.Convert(transferType)
	}
	return transferValue.Interface()
}

func (c *transferTypeConverter[Value, TransferValue]) ToTransferValue(value any) (any, error) {
	if value, ok := value.(Value); ok {
		return c.toTransferValue(value)
	}
	return value, nil
}

func (c *transferTypeConverter[Value, TransferValue]) FromTransferValue(transferValue any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		var expectedValue *Value
		return fmt.Errorf("Expected type %T, got %T", expectedValue, valuePtr)
	}
	t, ok := transferValue.(TransferValue)
	if !ok {
		var expectedTransfer TransferValue
		return fmt.Errorf("Expected transfer type %T, got %T", expectedTransfer, transferValue)
	}
	return c.fromTransferValue(t, v)
}

// -- DATA CONVERTERS ----------------------------------------------------------

// transferTypeDataConverter is a context-aware data converter that:
//
//  1. Encodes its input by trying to apply transfer conversion and forwarding
//     the result to the parent data converter; and
//  2. Decodes its input using the parent data converter and then trying to
//     transfer-convert the result into a normal value.
type transferTypeDataConverter struct {
	parent converter.DataConverter
}

var _ converter.DataConverter = (*transferTypeDataConverter)(nil)

// var _ converter.DataConverterWithSerializationContext = (*transferTypeDataConverter)(nil)
// var _ ContextAware = (*transferTypeDataConverter)(nil)

// toTransferTypeDataConverter is an idempotent operation that upgrades a
// normal data converter into a transfer-type-aware data converter.
func toTransferTypeDataConverter(dc converter.DataConverter) converter.DataConverter {
	if dc == nil {
		panic("nil data converter")
	}
	if _, ok := dc.(*transferTypeDataConverter); ok {
		return dc
	}
	return &transferTypeDataConverter{parent: dc}
}

func (dc *transferTypeDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	transferValue, err := transferTypeTryEncoding(value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferValue)
}

func (dc *transferTypeDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	for i, value := range values {
		transferValue, err := transferTypeTryEncoding(value)
		if err != nil {
			return nil, err
		}
		values[i] = transferValue
	}
	return dc.parent.ToPayloads(values...)
}

func transferTypeTryEncoding(value any) (transferValue any, err error) {
	convertible, ok := value.(TransferTypeConvertible)
	if !ok {
		return value, nil
	}
	return convertible.TransferTypeConverter().ToTransferValue(value)
}

func (dc *transferTypeDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	convertible, ok := valuePtr.(TransferTypeConvertible)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	transferPtr := convertible.TransferTypeConverter().NewTransferValuePtr()
	err := dc.parent.FromPayload(payload, transferPtr)
	if err != nil {
		return err
	}
	return convertible.TransferTypeConverter().FromTransferValue(transferPtr, valuePtr)
}

func (dc *transferTypeDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return nil
	}

	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		err := dc.FromPayload(payload, valuePtrs[i])
		if err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
	}

	return nil
}

func (dc *transferTypeDataConverter) ToString(input *commonpb.Payload) string {
	return dc.parent.ToString(input)
}

func (dc *transferTypeDataConverter) ToStrings(input *commonpb.Payloads) []string {
	return dc.parent.ToStrings(input)
}
