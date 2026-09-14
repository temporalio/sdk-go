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
// equals tc. Likewise, the SDK will only invoke tc.FromTransferValue(tvp, v)
// if v.TransferConverter() equals tc and tvp was obtained from
// tc.NewTransferValuePtr().
//
// NOTE: Experimental.
type TransferConverter interface {
	// NewTransferValuePtr returns a pointer to a zero transfer value.
	NewTransferValuePtr() any

	// ToTransferValue converts value into its serializable transfer value.
	ToTransferValue(ctx context.Context, value any) (any, error)

	// FromTransferValue converts a deserialized transfer value into valuePtr.
	// transferValuePtr is a pointer to the transfer value, as returned by
	// NewTransferValuePtr.
	FromTransferValue(ctx context.Context, transferValuePtr any, valuePtr any) error

	transferConverter()
}

// NewTransferConverter builds a [TransferConverter] that can map
// something of type Value into a serializable "transfer value", and back.
//
// NOTE: Experimental.
func NewTransferConverter[Value, TransferValue any](
	toTransferValue func(context.Context, Value) (TransferValue, error),
	fromTransferValue func(context.Context, TransferValue, *Value) error,
) TransferConverter {
	return &transferConverter[Value, TransferValue]{
		toTransferValue:   toTransferValue,
		fromTransferValue: fromTransferValue,
	}
}

type transferConverter[Value, TransferValue any] struct {
	toTransferValue   func(context.Context, Value) (TransferValue, error)
	fromTransferValue func(context.Context, TransferValue, *Value) error
}

func (*transferConverter[Value, TransferValue]) transferConverter() {}

func (*transferConverter[Value, TransferValue]) NewTransferValuePtr() any {
	return new(TransferValue)
}

func (tc *transferConverter[Value, TransferValue]) ToTransferValue(ctx context.Context, value any) (any, error) {
	v, ok := value.(Value)
	if !ok {
		// The SDK should only call ToTransferValue on v if v.TransferConverter()
		// equals tc. If we got here, we violated that contract.
		var zero Value
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", zero, value))
	}
	return tc.toTransferValue(ctx, v)
}

func (tc *transferConverter[Value, TransferValue]) FromTransferValue(ctx context.Context, transferValuePtr any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		// The SDK should only call FromTransferValue on v if v.TransferConverter()
		// equals tc. If we got here, we violated that contract.
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", (*Value)(nil), valuePtr))
	}
	tvp, ok := transferValuePtr.(*TransferValue)
	if !ok {
		// The SDK should only call FromTransferValue on tvp if tvp was obtained
		// from tc.NewTransferValuePtr(). If we got here, we violated that contract.
		panic(fmt.Sprintf("transfer converter: want transfer value of type %T, got %T", (*TransferValue)(nil), transferValuePtr))
	}
	return tc.fromTransferValue(ctx, *tvp, v)
}

// -- DATA CONVERTERS ----------------------------------------------------------

// transferAwareDataConverter is a context-aware data converter that:
//
//  1. Encodes its input by trying to apply transfer conversion and forwarding
//     the result to the parent data converter; and
//  2. Decodes its input using the parent data converter and then trying to
//     transfer-convert the result into a normal value.
type transferAwareDataConverter struct {
	parent  converter.DataConverter
	context context.Context
}

var _ converter.DataConverter = (*transferAwareDataConverter)(nil)
var _ converter.DataConverterWithSerializationContext = (*transferAwareDataConverter)(nil)
var _ ContextAware = (*transferAwareDataConverter)(nil)

// makeTransferAware is an idempotent operation that upgrades a
// normal data converter into a transfer-type-aware data converter.
func makeTransferAware(dc converter.DataConverter) *transferAwareDataConverter {
	if dc == nil {
		panic("nil data converter")
	}
	if tadc, ok := dc.(*transferAwareDataConverter); ok {
		return tadc
	}
	return &transferAwareDataConverter{parent: dc}
}

func (dc *transferAwareDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	transferValue, err := encodeAsTransferValueOrReturn(dc.context, value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferValue)
}

func (dc *transferAwareDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	// TODO Would callers be surprised if we mutated their array? See encodeArgs for instance.
	// Is it worth getting fancy to avoid this allocation in the common case where none of
	// the values are transfer-convertible?
	transferValues := make([]any, len(values))
	for i, value := range values {
		transferValue, err := encodeAsTransferValueOrReturn(dc.context, value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		transferValues[i] = transferValue
	}
	return dc.parent.ToPayloads(transferValues...)
}

func encodeAsTransferValueOrReturn(ctx context.Context, value any) (transferValue any, err error) {
	convertible, ok := value.(ValueWithTransferConverter)
	if !ok {
		return value, nil
	}
	return convertible.TransferConverter().ToTransferValue(ctx, value)
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return nil
	}
	convertible, ok := valuePtr.(ValueWithTransferConverter)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	tc := convertible.TransferConverter()
	transferValuePtr := tc.NewTransferValuePtr()
	err := dc.parent.FromPayload(payload, transferValuePtr)
	if err != nil {
		return err
	}
	return tc.FromTransferValue(dc.context, transferValuePtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	// TODO Is it worth getting fancy to avoid this allocation in the common case where
	// none of the values are transfer-convertible?
	transferValuePtrs := make([]any, len(valuePtrs))
	for i, valuePtr := range valuePtrs {
		convertible, ok := valuePtr.(ValueWithTransferConverter)
		if !ok {
			transferValuePtrs[i] = valuePtr
		} else {
			transferValuePtrs[i] = convertible.TransferConverter().NewTransferValuePtr()
		}
	}

	if err := dc.parent.FromPayloads(payloads, transferValuePtrs...); err != nil {
		return err
	}

	for i, valuePtr := range valuePtrs {
		convertible, ok := valuePtr.(ValueWithTransferConverter)
		if !ok {
			valuePtrs[i] = transferValuePtrs[i]
		} else {
			err := convertible.TransferConverter().
				FromTransferValue(dc.context, transferValuePtrs[i], valuePtrs[i])
			if err != nil {
				return fmt.Errorf("transfer converter: payload item %d: %w", i, err)
			}
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
	if _, ok := dc.parent.(converter.DataConverterWithSerializationContext); !ok {
		return dc
	}
	return &transferAwareDataConverter{
		parent:  converter.WithDataConverterSerializationContext(dc.parent, ctx),
		context: dc.context,
	}
}

func (dc *transferAwareDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	if parent, ok := dc.parent.(ContextAware); ok {
		return &transferAwareDataConverter{
			parent:  parent.WithWorkflowContext(ctx),
			context: dc.context,
		}
	}
	return dc
}

func (dc *transferAwareDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	parent := dc.parent
	if contextAwareParent, ok := parent.(ContextAware); ok {
		parent = contextAwareParent.WithContext(ctx)
	}
	return &transferAwareDataConverter{
		parent:  parent,
		context: ctx,
	}
}
