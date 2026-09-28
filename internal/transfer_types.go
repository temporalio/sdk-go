package internal

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

var DefaultInternalDataConverter = makeTransferAware(converter.GetDefaultDataConverter())

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
//
// Exposed as: [go.temporal.io/sdk/workflow.ValueWithTransferConverter]
type ValueWithTransferConverter interface {
	TransferConverter() TransferConverter
}

// TransferConverter converts application values to serializable transfer
// values and back. Create one using [NewTransferConverter] or
// [NewContextAwareTransferConverter].
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.TransferConverter]
type TransferConverter interface {
	// newTransferValuePtr returns a pointer to a zero transfer value.
	newTransferValuePtr() any

	// toTransferValue converts value into its serializable transfer value.
	toTransferValue(value any) (any, error)

	// fromTransferValue converts a deserialized transfer value into valuePtr.
	// transferValuePtr is a pointer to the transfer value, as returned by
	// NewTransferValuePtr.
	fromTransferValue(transferValuePtr any, valuePtr any) error

	toTransferValueWithContext(ctx context.Context, value any) (any, error)
	fromTransferValueWithContext(ctx context.Context, transferValuePtr any, valuePtr any) error

	toTransferValueWithWorkflowContext(ctx Context, value any) (any, error)
	fromTransferValueWithWorkflowContext(ctx Context, transferValuePtr any, valuePtr any) error
}

// NewContextAwareTransferConverter builds a [TransferConverter] that can map
// something of type Value into a serializable "transfer value", and back.
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.NewContextAwareTransferConverter]
func NewContextAwareTransferConverter[Value, TransferValue any](
	toTransferValue func(Value) (TransferValue, error),
	fromTransferValue func(TransferValue, *Value) error,
	toTransferValueWithContext func(context.Context, Value) (TransferValue, error),
	fromTransferValueWithContext func(context.Context, TransferValue, *Value) error,
	toTransferValueWithWorkflowContext func(Context, Value) (TransferValue, error),
	fromTransferValueWithWorkflowContext func(Context, TransferValue, *Value) error,
) TransferConverter {
	return &transferConverter[Value, TransferValue]{
		toTransferValueFn:                      toTransferValue,
		fromTransferValueFn:                    fromTransferValue,
		toTransferValueWithContextFn:           toTransferValueWithContext,
		fromTransferValueWithContextFn:         fromTransferValueWithContext,
		toTransferValueWithWorkflowContextFn:   toTransferValueWithWorkflowContext,
		fromTransferValueWithWorkflowContextFn: fromTransferValueWithWorkflowContext,
	}
}

// NewTransferConverter builds a [TransferConverter] that can map
// something of type Value into a serializable "transfer value", and back.
//
// Exposed as: [go.temporal.io/sdk/workflow.NewTransferConverter]
func NewTransferConverter[Value, TransferValue any](
	toTransferValue func(Value) (TransferValue, error),
	fromTransferValue func(TransferValue, *Value) error,
) TransferConverter {
	return NewContextAwareTransferConverter(
		toTransferValue,
		fromTransferValue,
		func(_ context.Context, value Value) (TransferValue, error) {
			return toTransferValue(value)
		},
		func(_ context.Context, transferValue TransferValue, valuePtr *Value) error {
			return fromTransferValue(transferValue, valuePtr)
		},
		func(_ Context, value Value) (TransferValue, error) {
			return toTransferValue(value)
		},
		func(_ Context, transferValue TransferValue, valuePtr *Value) error {
			return fromTransferValue(transferValue, valuePtr)
		},
	)
}

type transferConverter[Value, TransferValue any] struct {
	toTransferValueFn                      func(Value) (TransferValue, error)
	fromTransferValueFn                    func(TransferValue, *Value) error
	toTransferValueWithContextFn           func(context.Context, Value) (TransferValue, error)
	fromTransferValueWithContextFn         func(context.Context, TransferValue, *Value) error
	toTransferValueWithWorkflowContextFn   func(Context, Value) (TransferValue, error)
	fromTransferValueWithWorkflowContextFn func(Context, TransferValue, *Value) error
}

func (*transferConverter[Value, TransferValue]) newTransferValuePtr() any {
	return new(TransferValue)
}

func (tc *transferConverter[Value, TransferValue]) toTransferValue(value any) (any, error) {
	v, ok := value.(Value)
	if !ok {
		var zero Value
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", zero, value))
	}
	return tc.toTransferValueFn(v)
}

func (tc *transferConverter[Value, TransferValue]) fromTransferValue(transferValuePtr any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", (*Value)(nil), valuePtr))
	}
	tvp, ok := transferValuePtr.(*TransferValue)
	if !ok {
		panic(fmt.Sprintf("transfer converter: want transfer value of type %T, got %T", (*TransferValue)(nil), transferValuePtr))
	}
	return tc.fromTransferValueFn(*tvp, v)
}

func (tc *transferConverter[Value, TransferValue]) toTransferValueWithContext(ctx context.Context, value any) (any, error) {
	v, ok := value.(Value)
	if !ok {
		var zero Value
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", zero, value))
	}
	return tc.toTransferValueWithContextFn(ctx, v)
}

func (tc *transferConverter[Value, TransferValue]) fromTransferValueWithContext(ctx context.Context, transferValuePtr any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", (*Value)(nil), valuePtr))
	}
	tvp, ok := transferValuePtr.(*TransferValue)
	if !ok {
		panic(fmt.Sprintf("transfer converter: want transfer value of type %T, got %T", (*TransferValue)(nil), transferValuePtr))
	}
	return tc.fromTransferValueWithContextFn(ctx, *tvp, v)
}

func (tc *transferConverter[Value, TransferValue]) toTransferValueWithWorkflowContext(ctx Context, value any) (any, error) {
	v, ok := value.(Value)
	if !ok {
		var zero Value
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", zero, value))
	}
	return tc.toTransferValueWithWorkflowContextFn(ctx, v)
}

func (tc *transferConverter[Value, TransferValue]) fromTransferValueWithWorkflowContext(ctx Context, transferValuePtr any, valuePtr any) error {
	v, ok := valuePtr.(*Value)
	if !ok {
		panic(fmt.Sprintf("transfer converter: want value of type %T, got %T", (*Value)(nil), valuePtr))
	}
	tvp, ok := transferValuePtr.(*TransferValue)
	if !ok {
		panic(fmt.Sprintf("transfer converter: want transfer value of type %T, got %T", (*TransferValue)(nil), transferValuePtr))
	}
	return tc.fromTransferValueWithWorkflowContextFn(ctx, *tvp, v)
}

// -- DATA CONVERTERS ----------------------------------------------------------

// transferAwareDataConverter is a context-aware data converter that:
//
//  1. Encodes its input by trying to apply transfer conversion and forwarding
//     the result to the parent data converter; and
//  2. Decodes its input using the parent data converter and then trying to
//     transfer-convert the result into a normal value.
type transferAwareDataConverter struct {
	parent             converter.DataConverter
	context            context.Context
	workflowContext    Context
	transferConverters *sync.Map
}

var _ converter.DataConverter = (*transferAwareDataConverter)(nil)
var _ converter.DataConverterWithSerializationContext = (*transferAwareDataConverter)(nil)
var _ ContextAware = (*transferAwareDataConverter)(nil)

// makeTransferAware is an idempotent operation that upgrades a
// normal data converter into a transfer-type-aware data converter.
// If dc is nil, it wraps the default data converter.
func makeTransferAware(dc converter.DataConverter) *transferAwareDataConverter {
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}
	if tadc, ok := dc.(*transferAwareDataConverter); ok {
		return tadc
	}
	return &transferAwareDataConverter{
		parent:             dc,
		transferConverters: new(sync.Map),
	}
}

func (dc *transferAwareDataConverter) transferConverter(value ValueWithTransferConverter) TransferConverter {
	valueType := reflect.TypeOf(value)
	if valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if tc, ok := dc.transferConverters.Load(valueType); ok {
		return tc.(TransferConverter)
	}
	tc := value.TransferConverter()
	cached, _ := dc.transferConverters.LoadOrStore(valueType, tc)
	return cached.(TransferConverter)
}

func (dc *transferAwareDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	transferValue, err := dc.encodeAsTransferValueOrReturn(value)
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
		transferValue, err := dc.encodeAsTransferValueOrReturn(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		transferValues[i] = transferValue
	}
	return dc.parent.ToPayloads(transferValues...)
}

func (dc *transferAwareDataConverter) encodeAsTransferValueOrReturn(value any) (transferValue any, err error) {
	convertible, ok := value.(ValueWithTransferConverter)
	if !ok {
		return value, nil
	}
	return dc.toTransferValue(dc.transferConverter(convertible), value)
}

// toTransferValue converts value with whichever flavor of conversion suits the context
// this data converter is running in.
func (dc *transferAwareDataConverter) toTransferValue(tc TransferConverter, value any) (any, error) {
	if dc.workflowContext != nil {
		return tc.toTransferValueWithWorkflowContext(dc.workflowContext, value)
	}
	if dc.context != nil {
		return tc.toTransferValueWithContext(dc.context, value)
	}
	return tc.toTransferValue(value)
}

// fromTransferValue is the [transferAwareDataConverter.toTransferValue] counterpart.
func (dc *transferAwareDataConverter) fromTransferValue(tc TransferConverter, transferValuePtr any, valuePtr any) error {
	if dc.workflowContext != nil {
		return tc.fromTransferValueWithWorkflowContext(dc.workflowContext, transferValuePtr, valuePtr)
	}
	if dc.context != nil {
		return tc.fromTransferValueWithContext(dc.context, transferValuePtr, valuePtr)
	}
	return tc.fromTransferValue(transferValuePtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	convertible, ok := valuePtr.(ValueWithTransferConverter)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	tc := dc.transferConverter(convertible)
	transferValuePtr := tc.newTransferValuePtr()
	err := dc.parent.FromPayload(payload, transferValuePtr)
	if err != nil {
		return err
	}
	return dc.fromTransferValue(tc, transferValuePtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return dc.parent.FromPayloads(payloads, valuePtrs...)
	}
	// TODO Is it worth getting fancy to avoid this allocation in the common case where
	// none of the values are transfer-convertible?
	transferValuePtrs := make([]any, len(valuePtrs))
	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		convertible, ok := valuePtrs[i].(ValueWithTransferConverter)
		if !ok {
			transferValuePtrs[i] = valuePtrs[i]
		} else {
			transferValuePtrs[i] = dc.transferConverter(convertible).newTransferValuePtr()
		}
	}

	if err := dc.parent.FromPayloads(payloads, transferValuePtrs...); err != nil {
		return err
	}

	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		convertible, ok := valuePtrs[i].(ValueWithTransferConverter)
		if !ok {
			valuePtrs[i] = transferValuePtrs[i]
		} else {
			err := dc.fromTransferValue(
				dc.transferConverter(convertible), transferValuePtrs[i], valuePtrs[i])
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

// WithSerializationContext forwards the serialization context to the parent data
// converter. Transfer converters have no use for it.
func (dc *transferAwareDataConverter) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	if _, ok := dc.parent.(converter.DataConverterWithSerializationContext); !ok {
		return dc
	}
	result := *dc
	result.parent = converter.WithDataConverterSerializationContext(dc.parent, ctx)
	return &result
}

func (dc *transferAwareDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	result := *dc
	result.parent = WithWorkflowContext(ctx, dc.parent)
	result.context = nil
	result.workflowContext = ctx
	return &result
}

func (dc *transferAwareDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	result := *dc
	result.parent = WithContext(ctx, dc.parent)
	result.context = ctx
	result.workflowContext = nil
	return &result
}
