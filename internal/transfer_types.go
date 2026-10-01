package internal

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

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
	TransferTypeConverter() (TransferTypeConverter, error)
}

// TransferTypeConverter converts application values to transfer
// values and back. Create one using [NewTransferTypeConverter].
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.TransferTypeConverter]
type TransferTypeConverter interface {
	// newTransferTypePtr returns a pointer to a zero transfer value.
	newTransferTypePtr() any

	// toTransferType converts value into its transfer value.
	toTransferType(ctx context.Context, value any) (any, error)

	// fromTransferType reads a transfer value from transferTypePtr
	// and writes its corresponding model value into valuePtr.
	fromTransferType(ctx context.Context, transferTypePtr any, valuePtr any) error

	toTransferTypeWithWorkflowContext(ctx Context, value any) (any, error)
	fromTransferTypeWithWorkflowContext(ctx Context, transferTypePtr any, valuePtr any) error
}

// NewTransferTypeConverter builds a [TransferTypeConverter] that can map
// Model values into Transfer values and back.
//
// Returns an error if Model or Transfer is a pointer type.
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.NewTransferTypeConverter]
func NewTransferTypeConverter[Model, Transfer any](
	toTransferType func(context.Context, *Model) (*Transfer, error),
	fromTransferType func(context.Context, *Transfer, *Model) error,
	toTransferTypeWithWorkflowContext func(Context, *Model) (*Transfer, error),
	fromTransferTypeWithWorkflowContext func(Context, *Transfer, *Model) error,
) (TransferTypeConverter, error) {
	modelType := reflect.TypeFor[Model]()
	if modelType.Kind() == reflect.Pointer {
		return nil, fmt.Errorf("transfer type converter: model type must not be a pointer, got %v", modelType)
	}
	transferType := reflect.TypeFor[Transfer]()
	if transferType.Kind() == reflect.Pointer {
		return nil, fmt.Errorf("transfer type converter: transfer type must not be a pointer, got %v", transferType)
	}
	return &transferTypeConverterImpl[Model, Transfer]{
		toTransferTypeFn:                      toTransferType,
		fromTransferTypeFn:                    fromTransferType,
		toTransferTypeWithWorkflowContextFn:   toTransferTypeWithWorkflowContext,
		fromTransferTypeWithWorkflowContextFn: fromTransferTypeWithWorkflowContext,
	}, nil
}

type transferTypeConverterImpl[Model, Transfer any] struct {
	toTransferTypeFn                      func(context.Context, *Model) (*Transfer, error)
	fromTransferTypeFn                    func(context.Context, *Transfer, *Model) error
	toTransferTypeWithWorkflowContextFn   func(Context, *Model) (*Transfer, error)
	fromTransferTypeWithWorkflowContextFn func(Context, *Transfer, *Model) error
}

func (*transferTypeConverterImpl[Model, Transfer]) newTransferTypePtr() any {
	return new(Transfer)
}

func (tc *transferTypeConverterImpl[Model, Transfer]) toTransferType(ctx context.Context, value any) (any, error) {
	if valuePtr, ok := value.(*Model); ok {
		return tc.toTransferTypeFn(ctx, valuePtr)
	}
	if value, ok := value.(Model); ok {
		return tc.toTransferTypeFn(ctx, &value)
	}
	var zero Model
	return nil, fmt.Errorf("transfer type converter: want value of type %T or %T, got %T", zero, (*Model)(nil), value)
}

func (tc *transferTypeConverterImpl[Model, Transfer]) toTransferTypeWithWorkflowContext(ctx Context, value any) (any, error) {
	if valuePtr, ok := value.(*Model); ok {
		return tc.toTransferTypeWithWorkflowContextFn(ctx, valuePtr)
	}
	if value, ok := value.(Model); ok {
		return tc.toTransferTypeWithWorkflowContextFn(ctx, &value)
	}
	var zero Model
	return nil, fmt.Errorf("transfer type converter: want value of type %T or %T, got %T", zero, (*Model)(nil), value)
}

func (tc *transferTypeConverterImpl[Model, Transfer]) fromTransferType(ctx context.Context, transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*Model)
	if !ok {
		return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*Model)(nil), valuePtr)
	}
	tvp, ok := transferTypePtr.(*Transfer)
	if !ok {
		return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*Transfer)(nil), transferTypePtr)
	}
	return tc.fromTransferTypeFn(ctx, tvp, v)
}

func (tc *transferTypeConverterImpl[Model, Transfer]) fromTransferTypeWithWorkflowContext(ctx Context, transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*Model)
	if !ok {
		return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*Model)(nil), valuePtr)
	}
	tvp, ok := transferTypePtr.(*Transfer)
	if !ok {
		return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*Transfer)(nil), transferTypePtr)
	}
	return tc.fromTransferTypeWithWorkflowContextFn(ctx, tvp, v)
}

// -- DATA CONVERTERS ----------------------------------------------------------

// The default data converter, wrapped so it supports transfer type conversion.
// For values that don't implement [ValueWithTransferTypeConverter], this data
// converter behaves the same as [converter.GetDefaultDataConverter].
var defaultTransferAwareDataConverter *transferAwareDataConverter =
	makeTransferAware(converter.GetDefaultDataConverter())

// transferAwareDataConverter wraps a parent data converter and applies
// transfer type conversion to values that implement
// [ValueWithTransferTypeConverter].
type transferAwareDataConverter struct {
	parent                 converter.DataConverter
	// context is only set if this data converter was created by
	// [ContextAware.WithContext]. We store it so we can pass it to the
	// transfer type converter.
	context                context.Context
	// workflowContext is only set if this data converter was created by
	// [ContextAware.WithWorkflowContext]. We store it so we can pass it to the
	// transfer type converter.
	workflowContext        Context
	// transferTypeConverters is a cache that maps transfer-convertible types
	// to their transfer type converters.
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

func (dc *transferAwareDataConverter) transferTypeConverter(value ValueWithTransferTypeConverter) (TransferTypeConverter, error) {
	valueType := reflect.TypeOf(value)
	if valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if tc, ok := dc.transferTypeConverters.Load(valueType); ok {
		return tc.(TransferTypeConverter), nil
	}
	tc, err := value.TransferTypeConverter()
	if err != nil {
		return nil, fmt.Errorf("transfer type converter for %v: %w", valueType, err)
	}
	if tc == nil {
		return nil, fmt.Errorf("transfer type converter for %v is nil", valueType)
	}
	cached, _ := dc.transferTypeConverters.LoadOrStore(valueType, tc)
	return cached.(TransferTypeConverter), nil
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
	tc, err := dc.transferTypeConverter(convertible)
	if err != nil {
		return nil, err
	}
	return dc.toTransferType(tc, value)
}

// toTransferType converts value with whichever flavor of conversion suits the context
// this data converter is running in.
func (dc *transferAwareDataConverter) toTransferType(tc TransferTypeConverter, value any) (any, error) {
	if dc.workflowContext != nil {
		return tc.toTransferTypeWithWorkflowContext(dc.workflowContext, value)
	}
	if dc.context != nil {
		return tc.toTransferType(dc.context, value)
	}
	return tc.toTransferType(context.Background(), value)
}

// fromTransferType is the [transferAwareDataConverter.toTransferType] counterpart.
func (dc *transferAwareDataConverter) fromTransferType(tc TransferTypeConverter, transferTypePtr any, valuePtr any) error {
	if dc.workflowContext != nil {
		return tc.fromTransferTypeWithWorkflowContext(dc.workflowContext, transferTypePtr, valuePtr)
	}
	if dc.context != nil {
		return tc.fromTransferType(dc.context, transferTypePtr, valuePtr)
	}
	return tc.fromTransferType(context.Background(), transferTypePtr, valuePtr)
}

// transferDecodeDestination adapts **T to *T only when *T provides a converter.
// Allocate the actual destination before discovery, preserving existing instances.
func transferDecodeDestination(valuePtr any) any {
	value := reflect.ValueOf(valuePtr)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return valuePtr
	}
	destination := value.Elem()
	if destination.Kind() != reflect.Pointer ||
		!destination.Type().Implements(reflect.TypeFor[ValueWithTransferTypeConverter]()) {
		return valuePtr
	}
	if destination.IsNil() {
		destination.Set(reflect.New(destination.Type().Elem()))
	}
	return destination.Interface()
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	valuePtr = transferDecodeDestination(valuePtr)
	convertible, ok := valuePtr.(ValueWithTransferTypeConverter)
	if !ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	tc, err := dc.transferTypeConverter(convertible)
	if err != nil {
		return err
	}
	transferTypePtr := tc.newTransferTypePtr()
	err = dc.parent.FromPayload(payload, transferTypePtr)
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
	destinations := make([]struct {
		valuePtr  any
		converter TransferTypeConverter
	}, len(valuePtrs))
	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		valuePtr := transferDecodeDestination(valuePtrs[i])
		convertible, ok := valuePtr.(ValueWithTransferTypeConverter)
		if !ok {
			transferTypePtrs[i] = valuePtrs[i]
		} else {
			tc, err := dc.transferTypeConverter(convertible)
			if err != nil {
				return fmt.Errorf("transfer type converter: payload item %d: %w", i, err)
			}
			destinations[i].valuePtr = valuePtr
			destinations[i].converter = tc
			transferTypePtrs[i] = tc.newTransferTypePtr()
		}
	}

	if err := dc.parent.FromPayloads(payloads, transferTypePtrs...); err != nil {
		return err
	}

	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		destination := destinations[i]
		if destination.converter == nil {
			valuePtrs[i] = transferTypePtrs[i]
		} else {
			err := dc.fromTransferType(
				destination.converter, transferTypePtrs[i], destination.valuePtr)
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
