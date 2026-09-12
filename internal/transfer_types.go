package internal

import (
	"fmt"
	"reflect"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- USER API -----------------------------------------------------------

// TransferConvertible provides a transfer type converter for values of
// this type. The SDK calls TransferTypeConverter before serialization and
// after deserialization.
//
// NOTE: Experimental.
type TransferConvertible interface {
	TransferConverter() TransferConverter
}

// TransferConverter converts application values to serializable transfer
// values and back. Implement it using [NewTransferConverter].
//
// NOTE: Experimental.
type TransferConverter interface {
	NewTransferValuePtr() any
	ToTransferValue(value any) (any, error)
	FromTransferValue(transferValue any, valuePtr any) error
	transferConverter()
}

// NewTransferConverter builds a [TransferConverter] that can map
// something of type Value into a serializable "transfer value", and back.
//
// NOTE: Experimental.
func NewTransferConverter[Value, TransferValue any](
	toTransferValue func(Value) (TransferValue, error),
	fromTransferValue func(TransferValue, *Value) error,
) TransferConverter {
	return &transferConverter[Value, TransferValue]{
		toTransferValue:   toTransferValue,
		fromTransferValue: fromTransferValue,
	}
}

type transferConverter[Value, TransferValue any] struct {
	toTransferValue   func(Value) (TransferValue, error)
	fromTransferValue func(TransferValue, *Value) error
}

func (*transferConverter[Value, TransferValue]) transferConverter() {}

func (*transferConverter[Value, TransferValue]) NewTransferValuePtr() any {
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

func (c *transferConverter[Value, TransferValue]) ToTransferValue(value any) (any, error) {
	if value, ok := value.(Value); ok {
		return c.toTransferValue(value)
	}
	return value, nil
}

func (c *transferConverter[Value, TransferValue]) FromTransferValue(transferValue any, valuePtr any) error {
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

// transferAwareDataConverter is a context-aware data converter that:
//
//  1. Encodes its input by trying to apply transfer conversion and forwarding
//     the result to the parent data converter; and
//  2. Decodes its input using the parent data converter and then trying to
//     transfer-convert the result into a normal value.
type transferAwareDataConverter struct {
	parent converter.DataConverter
}

var _ converter.DataConverter = (*transferAwareDataConverter)(nil)

// var _ converter.DataConverterWithSerializationContext = (*transferTypeDataConverter)(nil)
// var _ ContextAware = (*transferTypeDataConverter)(nil)

// makeTransferAware is an idempotent operation that upgrades a
// normal data converter into a transfer-type-aware data converter.
func makeTransferAware(dc converter.DataConverter) converter.DataConverter {
	if dc == nil {
		panic("nil data converter")
	}
	if _, ok := dc.(*transferAwareDataConverter); ok {
		return dc
	}
	return &transferAwareDataConverter{parent: dc}
}

func (dc *transferAwareDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	transferValue, err := toTransferValue(value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferValue)
}

func (dc *transferAwareDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	for i, value := range values {
		transferValue, err := toTransferValue(value)
		if err != nil {
			return nil, err
		}
		values[i] = transferValue
	}
	return dc.parent.ToPayloads(values...)
}

func toTransferValue(value any) (transferValue any, err error) {
	convertible, ok := value.(TransferConvertible)
	if !ok {
		return value, nil
	}
	return convertible.TransferConverter().ToTransferValue(value)
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	convertible, ok := valuePtr.(TransferConvertible)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	transferPtr := convertible.TransferConverter().NewTransferValuePtr()
	err := dc.parent.FromPayload(payload, transferPtr)
	if err != nil {
		return err
	}
	return convertible.TransferConverter().FromTransferValue(transferPtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
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

func (dc *transferAwareDataConverter) ToString(input *commonpb.Payload) string {
	return dc.parent.ToString(input)
}

func (dc *transferAwareDataConverter) ToStrings(input *commonpb.Payloads) []string {
	return dc.parent.ToStrings(input)
}
