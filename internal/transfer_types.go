package internal

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// The default data converter, wrapped so it also supports transfer-type conversion.
var defaultTransferAwareDataConverter = makeTransferAware(converter.GetDefaultDataConverter())

// -- USER API -----------------------------------------------------------

// ValueWithTransferTypeConverter is an optional interface that values can implement to provide
// the SDK with a transfer type converter.
//
// When implemented, the SDK calls [ValueWithTransferTypeConverter.TransferTypeConverter] before
// serializing the value. The returned TransferTypeConverter will be used to turn the value
// into a serializable representation, called a transfer value. The converter will also
// be used to turn the transfer value back into the original value after deserialization.
//
// When the SDK first encounters a value of type T that implements this interface, the SDK
// will cache the returned transfer type converter. The SDK will try to use the cached
// converter for all subsequent values of type T. Hence values of the same type should
// also have the same transfer type converter.
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.ValueWithTransferTypeConverter]
type ValueWithTransferTypeConverter interface {
	TransferTypeConverter() TransferTypeConverter
}

// TransferTypeConverter converts application values to serializable transfer
// values and back. Create one using [NewTransferTypeConverter] or
// [NewContextAwareTransferTypeConverter].
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.TransferTypeConverter]
type TransferTypeConverter interface {
	// newTransferTypePtr returns a pointer to a zero transfer value.
	newTransferTypePtr() any

	// toTransferType converts value into its serializable transfer value.
	toTransferType(value any) (any, error)

	// fromTransferType converts a deserialized transfer value into valuePtr.
	// transferTypePtr is a pointer to the transfer value, as returned by
	// newTransferTypePtr.
	fromTransferType(transferTypePtr any, valuePtr any) error

	toTransferTypeWithContext(ctx context.Context, value any) (any, error)
	fromTransferTypeWithContext(ctx context.Context, transferTypePtr any, valuePtr any) error

	toTransferTypeWithWorkflowContext(ctx Context, value any) (any, error)
	fromTransferTypeWithWorkflowContext(ctx Context, transferTypePtr any, valuePtr any) error
}

// NewContextAwareTransferTypeConverter builds a [TransferTypeConverter] that can map
// something of type ModelType into a serializable "transfer value", and back.
// ModelType must not be a pointer type.
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.NewContextAwareTransferTypeConverter]
func NewContextAwareTransferTypeConverter[ModelType, TransferType any](
	toTransferType func(*ModelType) (*TransferType, error),
	fromTransferType func(*TransferType, *ModelType) error,
	toTransferTypeWithContext func(context.Context, *ModelType) (*TransferType, error),
	fromTransferTypeWithContext func(context.Context, *TransferType, *ModelType) error,
	toTransferTypeWithWorkflowContext func(Context, *ModelType) (*TransferType, error),
	fromTransferTypeWithWorkflowContext func(Context, *TransferType, *ModelType) error,
) TransferTypeConverter {
	modelType := reflect.TypeFor[ModelType]()
	if modelType.Kind() == reflect.Pointer {
		panic(fmt.Sprintf("transfer type converter: ModelType must not be a pointer, got %v", modelType))
	}
	return &transferTypeConverter[ModelType, TransferType]{
		toTransferTypeFn:                      toTransferType,
		fromTransferTypeFn:                    fromTransferType,
		toTransferTypeWithContextFn:           toTransferTypeWithContext,
		fromTransferTypeWithContextFn:         fromTransferTypeWithContext,
		toTransferTypeWithWorkflowContextFn:   toTransferTypeWithWorkflowContext,
		fromTransferTypeWithWorkflowContextFn: fromTransferTypeWithWorkflowContext,
	}
}

// NewTransferTypeConverter builds a [TransferTypeConverter] that can map
// something of type ModelType into a serializable "transfer value", and back.
// ModelType must not be a pointer type.
//
// Exposed as: [go.temporal.io/sdk/workflow.NewTransferTypeConverter]
func NewTransferTypeConverter[ModelType, TransferType any](
	toTransferType func(*ModelType) (*TransferType, error),
	fromTransferType func(*TransferType, *ModelType) error,
) TransferTypeConverter {
	return NewContextAwareTransferTypeConverter(
		toTransferType,
		fromTransferType,
		func(_ context.Context, value *ModelType) (*TransferType, error) {
			return toTransferType(value)
		},
		func(_ context.Context, transferType *TransferType, valuePtr *ModelType) error {
			return fromTransferType(transferType, valuePtr)
		},
		func(_ Context, value *ModelType) (*TransferType, error) {
			return toTransferType(value)
		},
		func(_ Context, transferType *TransferType, valuePtr *ModelType) error {
			return fromTransferType(transferType, valuePtr)
		},
	)
}

type transferTypeConverter[ModelType, TransferType any] struct {
	toTransferTypeFn                      func(*ModelType) (*TransferType, error)
	fromTransferTypeFn                    func(*TransferType, *ModelType) error
	toTransferTypeWithContextFn           func(context.Context, *ModelType) (*TransferType, error)
	fromTransferTypeWithContextFn         func(context.Context, *TransferType, *ModelType) error
	toTransferTypeWithWorkflowContextFn   func(Context, *ModelType) (*TransferType, error)
	fromTransferTypeWithWorkflowContextFn func(Context, *TransferType, *ModelType) error
}

func (*transferTypeConverter[ModelType, TransferType]) newTransferTypePtr() any {
	return new(TransferType)
}

func modelTypePtr[ModelType any](value any) *ModelType {
	if valuePtr, ok := value.(*ModelType); ok {
		return valuePtr
	}
	if value, ok := value.(ModelType); ok {
		return &value
	}
	var zero ModelType
	panic(fmt.Sprintf("transfer type converter: want value of type %T or %T, got %T", zero, (*ModelType)(nil), value))
}

func (tc *transferTypeConverter[ModelType, TransferType]) toTransferType(value any) (any, error) {
	return tc.toTransferTypeFn(modelTypePtr[ModelType](value))
}

func (tc *transferTypeConverter[ModelType, TransferType]) fromTransferType(transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*ModelType)
	if !ok {
		panic(fmt.Sprintf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr))
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		panic(fmt.Sprintf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr))
	}
	return tc.fromTransferTypeFn(tvp, v)
}

func (tc *transferTypeConverter[ModelType, TransferType]) toTransferTypeWithContext(ctx context.Context, value any) (any, error) {
	return tc.toTransferTypeWithContextFn(ctx, modelTypePtr[ModelType](value))
}

func (tc *transferTypeConverter[ModelType, TransferType]) fromTransferTypeWithContext(ctx context.Context, transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*ModelType)
	if !ok {
		panic(fmt.Sprintf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr))
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		panic(fmt.Sprintf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr))
	}
	return tc.fromTransferTypeWithContextFn(ctx, tvp, v)
}

func (tc *transferTypeConverter[ModelType, TransferType]) toTransferTypeWithWorkflowContext(ctx Context, value any) (any, error) {
	return tc.toTransferTypeWithWorkflowContextFn(ctx, modelTypePtr[ModelType](value))
}

func (tc *transferTypeConverter[ModelType, TransferType]) fromTransferTypeWithWorkflowContext(ctx Context, transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*ModelType)
	if !ok {
		panic(fmt.Sprintf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr))
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		panic(fmt.Sprintf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr))
	}
	return tc.fromTransferTypeWithWorkflowContextFn(ctx, tvp, v)
}

// -- DATA CONVERTERS ----------------------------------------------------------

// transferAwareDataConverter is a context-aware data converter that:
//
//  1. Encodes its input by trying to apply transfer conversion and forwarding
//     the result to the parent data converter; and
//  2. Decodes its input using the parent data converter and then trying to
//     transfer-convert the result into a normal value.
type transferAwareDataConverter struct {
	parent                 converter.DataConverter
	context                context.Context
	workflowContext        Context
	transferTypeConverters *sync.Map
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
		parent:                 dc,
		transferTypeConverters: new(sync.Map),
	}
}

func (dc *transferAwareDataConverter) transferTypeConverter(value ValueWithTransferTypeConverter) TransferTypeConverter {
	valueType := reflect.TypeOf(value)
	if valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if tc, ok := dc.transferTypeConverters.Load(valueType); ok {
		return tc.(TransferTypeConverter)
	}
	tc := value.TransferTypeConverter()
	cached, _ := dc.transferTypeConverters.LoadOrStore(valueType, tc)
	return cached.(TransferTypeConverter)
}

func (dc *transferAwareDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	transferType, err := dc.encodeAsTransferTypeOrReturn(value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferType)
}

func (dc *transferAwareDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	// TODO Would callers be surprised if we mutated their array? See encodeArgs for instance.
	// Is it worth getting fancy to avoid this allocation in the common case where none of
	// the values are transfer-convertible?
	transferTypes := make([]any, len(values))
	for i, value := range values {
		transferType, err := dc.encodeAsTransferTypeOrReturn(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		transferTypes[i] = transferType
	}
	return dc.parent.ToPayloads(transferTypes...)
}

func (dc *transferAwareDataConverter) encodeAsTransferTypeOrReturn(value any) (transferType any, err error) {
	convertible, ok := value.(ValueWithTransferTypeConverter)
	if !ok {
		return value, nil
	}
	return dc.toTransferType(dc.transferTypeConverter(convertible), value)
}

// toTransferType converts value with whichever flavor of conversion suits the context
// this data converter is running in.
func (dc *transferAwareDataConverter) toTransferType(tc TransferTypeConverter, value any) (any, error) {
	if dc.workflowContext != nil {
		return tc.toTransferTypeWithWorkflowContext(dc.workflowContext, value)
	}
	if dc.context != nil {
		return tc.toTransferTypeWithContext(dc.context, value)
	}
	return tc.toTransferType(value)
}

// fromTransferType is the [transferAwareDataConverter.toTransferType] counterpart.
func (dc *transferAwareDataConverter) fromTransferType(tc TransferTypeConverter, transferTypePtr any, valuePtr any) error {
	if dc.workflowContext != nil {
		return tc.fromTransferTypeWithWorkflowContext(dc.workflowContext, transferTypePtr, valuePtr)
	}
	if dc.context != nil {
		return tc.fromTransferTypeWithContext(dc.context, transferTypePtr, valuePtr)
	}
	return tc.fromTransferType(transferTypePtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	convertible, ok := valuePtr.(ValueWithTransferTypeConverter)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	tc := dc.transferTypeConverter(convertible)
	transferTypePtr := tc.newTransferTypePtr()
	err := dc.parent.FromPayload(payload, transferTypePtr)
	if err != nil {
		return err
	}
	return dc.fromTransferType(tc, transferTypePtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return dc.parent.FromPayloads(payloads, valuePtrs...)
	}
	// TODO Is it worth getting fancy to avoid this allocation in the common case where
	// none of the values are transfer-convertible?
	transferTypePtrs := make([]any, len(valuePtrs))
	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		convertible, ok := valuePtrs[i].(ValueWithTransferTypeConverter)
		if !ok {
			transferTypePtrs[i] = valuePtrs[i]
		} else {
			transferTypePtrs[i] = dc.transferTypeConverter(convertible).newTransferTypePtr()
		}
	}

	if err := dc.parent.FromPayloads(payloads, transferTypePtrs...); err != nil {
		return err
	}

	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		convertible, ok := valuePtrs[i].(ValueWithTransferTypeConverter)
		if !ok {
			valuePtrs[i] = transferTypePtrs[i]
		} else {
			err := dc.fromTransferType(
				dc.transferTypeConverter(convertible), transferTypePtrs[i], valuePtrs[i])
			if err != nil {
				return fmt.Errorf("transfer type converter: payload item %d: %w", i, err)
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
