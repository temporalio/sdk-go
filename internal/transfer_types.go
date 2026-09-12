package internal

import (
	"fmt"
	"reflect"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- USER API -----------------------------------------------------------

// TransferTypeConvertible is a marker interface for transfer-convertible values.
//
// NOTE: Experimental.
type TransferTypeConvertible interface {
	TransferTypeConverter() TransferTypeConverter
}

// TransferTypeConverter is something that converts application values to
// transfer values. Create one using [NewTransferTypeConverter].
//
// NOTE: Experimental.
type TransferTypeConverter interface {
	NewTransferType() any
	ToTransferType(value any) (any, error)
	FromTransferType(deserialized any, valuePtr any) error
}

// NewTransferTypeConverter builds a [TransferTypeConverter] that can map
// something of type Value into a serializable "transfer value", and back.
//
// NOTE: Experimental.
func NewTransferTypeConverter[Value, Transfer any](
	toTransferValue func(Value) (Transfer, error),
	fromTransferValue func(Transfer, *Value) error,
) TransferTypeConverter {
	return &transferTypeConverter[Value, Transfer]{
		toTransferValue:   toTransferValue,
		fromTransferValue: fromTransferValue,
	}
}

type transferTypeConverter[Value, Transfer any] struct {
	toTransferValue   func(Value) (Transfer, error)
	fromTransferValue func(Transfer, *Value) error
}

func (*transferTypeConverter[Value, Transfer]) NewTransferType() any {
	transferType := reflect.TypeFor[Transfer]()
	if transferType.Kind() != reflect.Pointer {
		return new(Transfer)
	}

	transferValue := reflect.New(transferType.Elem())
	if transferValue.Type() != transferType {
		transferValue = transferValue.Convert(transferType)
	}
	return transferValue.Interface()
}

func (c *transferTypeConverter[Value, Transfer]) ToTransferType(value any) (any, error) {
	if value, ok := value.(Value); ok {
		return c.toTransferValue(value)
	}
	return value, nil
}

func (c *transferTypeConverter[Value, Transfer]) FromTransferType(transferValue any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		var expectedValue *Value
		return fmt.Errorf("Expected type %T, got %T", expectedValue, valuePtr)
	}
	t, ok := transferValue.(Transfer)
	if !ok {
		var expectedTransfer Transfer
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
	return convertible.TransferTypeConverter().ToTransferType(value)
}

func (dc *transferTypeDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	convertible, ok := valuePtr.(TransferTypeConvertible)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	transferPtr := convertible.TransferTypeConverter().NewTransferType()
	err := dc.parent.FromPayload(payload, transferPtr)
	if err != nil {
		return err
	}
	return convertible.TransferTypeConverter().FromTransferType(transferPtr, valuePtr)
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
