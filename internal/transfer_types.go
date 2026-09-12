package internal

import (
	"context"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- USER API -----------------------------------------------------------

// ValueWithTransferConverter is an optional interface that values can implement to provide
// the SDK with a transfer converter.
//
// When implemented, the SDK calls [ValueWithTransferConverter.TransferConverter] before
// serializing the value. The returned TransferConverter will be used to turn the value
// into a serializable representation, called a transfer value. The converter will also
// be used to turn the transfer value back into the original value after deserialization.
//
// This method should be cheap and fast; the SDK may call this method frequently.
//
// NOTE: Experimental.
type ValueWithTransferConverter interface {
	TransferConverter() TransferConverter
}

// TransferConverter converts application values to serializable transfer
// values and back. Implement it using [NewTransferConverter].
//
// The SDK will only invoke tc.ToTransferValue(v) if v.TransferConverter()
// equals tc. Likewise, the SDK will only invoke tc.FromTransferValue(tv, v)
// if v.TransferConverter() equals tc and tv was obtained from
// tc.NewTransferValuePtr().
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
	return new(TransferValue)
}

func (tc *transferConverter[Value, TransferValue]) ToTransferValue(value any) (any, error) {
	if value, ok := value.(Value); ok {
		return tc.toTransferValue(value)
	}
	// The SDK should only call ToTransferValue on v if v.TransferConverter() equals c.
	// If we got here, we violated that contract.
	panic(fmt.Sprintf("Expected type %T, got %T", *new(Value), value))
}

func (tc *transferConverter[Value, TransferValue]) FromTransferValue(transferValue any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		// The SDK should only call FromTransferValue on v if v.TransferConverter() equals c.
		// If we got here, we violated that contract.
		var expectedValue *Value
		panic(fmt.Errorf("Expected type %T, got %T", expectedValue, valuePtr))
	}
	t, ok := transferValue.(TransferValue)
	if !ok {
		// The SDK should only call FromTransferValue on tv if tv was obtained from
		// tc.NewTransferValuePtr().
		// If we got here, we violated that contract.
		var expectedTransfer TransferValue
		panic(fmt.Errorf("Expected transfer type %T, got %T", expectedTransfer, transferValue))
	}
	return tc.fromTransferValue(t, v)
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
var _ converter.DataConverterWithSerializationContext = (*transferAwareDataConverter)(nil)
var _ ContextAware = (*transferAwareDataConverter)(nil)

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
	transferValue, err := encodeAsTransferValueOrReturn(value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferValue)
}

func (dc *transferAwareDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	for i, value := range values {
		transferValue, err := encodeAsTransferValueOrReturn(value)
		if err != nil {
			return nil, err
		}
		values[i] = transferValue
	}
	return dc.parent.ToPayloads(values...)
}

func encodeAsTransferValueOrReturn(value any) (transferValue any, err error) {
	convertible, ok := value.(ValueWithTransferConverter)
	if !ok {
		return value, nil
	}
	return convertible.TransferConverter().ToTransferValue(value)
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	convertible, ok := valuePtr.(ValueWithTransferConverter)
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

func (dc *transferAwareDataConverter) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	if parent, ok := dc.parent.(converter.DataConverterWithSerializationContext); ok {
		return &transferAwareDataConverter{
			parent: parent.WithSerializationContext(ctx),
		}
	}
	return dc
}

func (dc *transferAwareDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	if parent, ok := dc.parent.(ContextAware); ok {
		return &transferAwareDataConverter{
			parent: parent.WithWorkflowContext(ctx),
		}
	}
	return dc
}

func (dc *transferAwareDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	if parent, ok := dc.parent.(ContextAware); ok {
		return &transferAwareDataConverter{
			parent: parent.WithContext(ctx),
		}
	}
	return dc
}