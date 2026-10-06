package converter

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// -- USER API -----------------------------------------------------------

// ValueWithTransferTypeConverter is a type that opts-in to transfer type conversion.
// To encode a ValueWithTransferTypeConverter value as a payload, the SDK encodes
// the value using its transfer type converter first and then passes the
// resulting "transfer value" to the data converter. To decode a
// ValueWithTransferTypeConverter, the SDK reads a transfer value from the data
// converter and decodes it with the transfer type converter.
//
// TransferTypeConverter returns a value's transfer type converter.
// Create one with [NewTransferTypeConverter] or [NewContextualTransferTypeConverter].
// The method must be pure and safe to call concurrently.
// The converter will be cached and reused for other
// values with the same concrete type, so the method should not depend on any
// state in the value itself. The method must be implemented with a value
// receiver, not a pointer receiver. Inheriting this method via embedding
// is not supported.
//
// Transfer conversion applies only to top-level payload values. For a
// non-pointer model type T, encode T or a non-nil *T and decode into a non-nil
// *T. Workflow and activity parameters must use T rather than *T. Nil model
// pointers and decoding into **T are unsupported; represent optional values
// explicitly in the model or transfer type instead.
//
// NOTE: Experimental.
type ValueWithTransferTypeConverter interface {
	TransferTypeConverter() (TransferTypeConverter, error)
}

// TransferTypeConverter is an opaque handle created by
// [NewTransferTypeConverter] or [NewContextualTransferTypeConverter]. Do not embed this interface.
//
// NOTE: Experimental.
type TransferTypeConverter interface {
	// newTransferTypePtr returns a pointer to a zero transfer value.
	newTransferTypePtr() any

	// toTransferType converts value into its transfer value.
	toTransferType(ctx context.Context, value any) (any, error)

	// fromTransferType reads a transfer value from transferTypePtr
	// and writes its corresponding model value into valuePtr.
	fromTransferType(ctx context.Context, transferTypePtr any, valuePtr any) error

	// toTransferTypeWithWorkflowContext converts value into its transfer value
	// using a workflow context.
	toTransferTypeWithWorkflowContext(ctx workflowcontext.Context, value any) (any, error)

	// fromTransferTypeWithWorkflowContext reads a transfer value from transferTypePtr
	// and writes its corresponding model value into valuePtr using a workflow context.
	fromTransferTypeWithWorkflowContext(ctx workflowcontext.Context, transferTypePtr any, valuePtr any) error
}

// NewTransferTypeConverter builds a transfer type converter that can map
// Model values into Transfer values and back. The callbacks should be
// pure, threadsafe, contain no workflow commands, and produce replay-stable output.
// The callbacks receive non-nil pointers to values. Encoding callbacks must
// return a non-nil transfer pointer on success.
//
// Returns an error if Model or Transfer is a pointer type.
//
// NOTE: Experimental.
func NewTransferTypeConverter[Model ValueWithTransferTypeConverter, Transfer any](
	toTransferType func(*Model) (*Transfer, error),
	fromTransferType func(*Transfer, *Model) error,
) (TransferTypeConverter, error) {
	return NewContextualTransferTypeConverter(
		func(_ context.Context, value *Model) (*Transfer, error) {
			return toTransferType(value)
		},
		func(_ context.Context, transfer *Transfer, value *Model) error {
			return fromTransferType(transfer, value)
		},
		func(_ workflowcontext.Context, value *Model) (*Transfer, error) {
			return toTransferType(value)
		},
		func(_ workflowcontext.Context, transfer *Transfer, value *Model) error {
			return fromTransferType(transfer, value)
		},
	)
}

// NewContextualTransferTypeConverter builds a transfer type converter that can map
// Model values into Transfer values and back using separate callbacks for Go and
// workflow contexts. The callbacks should be pure, threadsafe, contain no workflow
// commands, and produce replay-stable output.
// The callbacks receive non-nil pointers to values. Encoding callbacks must
// return a non-nil transfer pointer on success.
//
// Returns an error if Model or Transfer is a pointer type.
//
// NOTE: Experimental.
func NewContextualTransferTypeConverter[Model ValueWithTransferTypeConverter, Transfer any](
	toTransferType func(context.Context, *Model) (*Transfer, error),
	fromTransferType func(context.Context, *Transfer, *Model) error,
	toTransferTypeWithWorkflowContext func(workflowcontext.Context, *Model) (*Transfer, error),
	fromTransferTypeWithWorkflowContext func(workflowcontext.Context, *Transfer, *Model) error,
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

type transferTypeConverterImpl[ModelType, TransferType any] struct {
	toTransferTypeFn                      func(context.Context, *ModelType) (*TransferType, error)
	fromTransferTypeFn                    func(context.Context, *TransferType, *ModelType) error
	toTransferTypeWithWorkflowContextFn   func(workflowcontext.Context, *ModelType) (*TransferType, error)
	fromTransferTypeWithWorkflowContextFn func(workflowcontext.Context, *TransferType, *ModelType) error
}

func (tc *transferTypeConverterImpl[ModelType, TransferType]) newTransferTypePtr() any {
	return new(TransferType)
}

func modelTypePtr[ModelType any](value any) (*ModelType, error) {
	if valuePtr, ok := value.(*ModelType); ok {
		return valuePtr, nil
	}
	if value, ok := value.(ModelType); ok {
		return &value, nil
	}
	var zero ModelType
	return nil, fmt.Errorf("transfer type converter: want value of type %T or %T, got %T", zero, (*ModelType)(nil), value)
}

func (tc *transferTypeConverterImpl[ModelType, TransferType]) toTransferType(ctx context.Context, value any) (any, error) {
	valuePtr, err := modelTypePtr[ModelType](value)
	if err != nil {
		return nil, err
	}
	return tc.toTransferTypeFn(ctx, valuePtr)
}

func (tc *transferTypeConverterImpl[ModelType, TransferType]) fromTransferType(ctx context.Context, transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*ModelType)
	if !ok {
		return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr)
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr)
	}
	return tc.fromTransferTypeFn(ctx, tvp, v)
}

func (tc *transferTypeConverterImpl[ModelType, TransferType]) toTransferTypeWithWorkflowContext(ctx workflowcontext.Context, value any) (any, error) {
	valuePtr, err := modelTypePtr[ModelType](value)
	if err != nil {
		return nil, err
	}
	return tc.toTransferTypeWithWorkflowContextFn(ctx, valuePtr)
}

func (tc *transferTypeConverterImpl[ModelType, TransferType]) fromTransferTypeWithWorkflowContext(ctx workflowcontext.Context, transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*ModelType)
	if !ok {
		return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr)
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr)
	}
	return tc.fromTransferTypeWithWorkflowContextFn(ctx, tvp, v)
}

// -- DATA CONVERTERS ----------------------------------------------------------

// transferAwareDataConverter wraps a parent data converter and applies
// transfer type conversion to values that implement
// [ValueWithTransferTypeConverter].
type transferAwareDataConverter struct {
	parent DataConverter
	// context is only set if this data converter was created by
	// [ContextAware.WithContext]. We store it so we can pass it to the
	// transfer type converter.
	context context.Context
	// workflowContext is only set if this data converter was created by
	// [ContextAware.WithWorkflowContext]. We store it so we can pass it to the
	// transfer type converter.
	workflowContext workflowcontext.Context
	// transferTypeConverters is a cache that maps transfer-convertible types
	// to their transfer type converters.
	transferTypeConverters *sync.Map
}

var _ DataConverter = (*transferAwareDataConverter)(nil)
var _ DataConverterWithSerializationContext = (*transferAwareDataConverter)(nil)
var _ ContextAware = (*transferAwareDataConverter)(nil)

// MakeTransferAware wraps dc to support transfer type conversion.
// If dc is nil, it returns [GetDefaultDataConverter]. If dc is already a
// transfer-aware wrapper, it returns dc unchanged.
//
// NOTE: Experimental.
func MakeTransferAware(dc DataConverter) DataConverter {
	if dc == nil {
		return GetDefaultDataConverter()
	}
	return newTransferAwareDataConverter(dc)
}

func newTransferAwareDataConverter(dc DataConverter) *transferAwareDataConverter {
	if tadc, ok := dc.(*transferAwareDataConverter); ok {
		return tadc
	}
	return &transferAwareDataConverter{
		parent:                 dc,
		transferTypeConverters: new(sync.Map),
	}
}

// transferTypeConverter fetches a converter from cache using the value's
// underlying type as a key, populating the cache if necessary.
func (dc *transferAwareDataConverter) transferTypeConverter(value ValueWithTransferTypeConverter) (TransferTypeConverter, error) {
	// The value's type is either T or *T for some underlying non-pointer type T.
	// A more deeply nested pointer type like **T couldn't implement the interface.
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
	transferType, _, err := dc.encodeAsTransferTypeOrReturn(value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferType)
}

func (dc *transferAwareDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	transferTypes := values
	copied := false
	for i, value := range values {
		transferType, converted, err := dc.encodeAsTransferTypeOrReturn(value)
		if err != nil {
			return nil, err
		}
		if !converted {
			continue
		}
		if !copied {
			transferTypes = slices.Clone(values)
			copied = true
		}
		transferTypes[i] = transferType
	}
	return dc.parent.ToPayloads(transferTypes...)
}

func (dc *transferAwareDataConverter) encodeAsTransferTypeOrReturn(value any) (any, bool, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return value, false, nil
	}
	convertible, ok := value.(ValueWithTransferTypeConverter)
	if !ok {
		return value, false, nil
	}
	tc, err := dc.transferTypeConverter(convertible)
	if err != nil {
		return nil, false, err
	}
	transferType, err := dc.toTransferType(tc, value)
	return transferType, true, err
}

func (dc *transferAwareDataConverter) toTransferType(tc TransferTypeConverter, value any) (any, error) {
	if dc.workflowContext != nil {
		return tc.toTransferTypeWithWorkflowContext(dc.workflowContext, value)
	}
	if dc.context != nil {
		return tc.toTransferType(dc.context, value)
	}
	return tc.toTransferType(context.Background(), value)
}

func (dc *transferAwareDataConverter) fromTransferType(tc TransferTypeConverter, transferTypePtr any, valuePtr any) error {
	if dc.workflowContext != nil {
		return tc.fromTransferTypeWithWorkflowContext(dc.workflowContext, transferTypePtr, valuePtr)
	}
	if dc.context != nil {
		return tc.fromTransferType(dc.context, transferTypePtr, valuePtr)
	}
	return tc.fromTransferType(context.Background(), transferTypePtr, valuePtr)
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
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
	transferTypePtrs := valuePtrs
	copied := false

	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		if payload == nil {
			continue
		}
		valuePtr := valuePtrs[i]
		convertible, ok := valuePtr.(ValueWithTransferTypeConverter)
		if !ok {
			continue
		}
		tc, err := dc.transferTypeConverter(convertible)
		if err != nil {
			return err
		}
		if !copied {
			transferTypePtrs = slices.Clone(valuePtrs)
			copied = true
		}
		transferTypePtrs[i] = tc.newTransferTypePtr()
	}

	if err := dc.parent.FromPayloads(payloads, transferTypePtrs...); err != nil {
		return err
	}

	if !copied {
		return nil
	}
	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		if payload == nil {
			continue
		}
		valuePtr := valuePtrs[i]
		convertible, ok := valuePtr.(ValueWithTransferTypeConverter)
		if !ok {
			continue
		}
		tc, err := dc.transferTypeConverter(convertible)
		if err != nil {
			return err
		}
		if err := dc.fromTransferType(tc, transferTypePtrs[i], valuePtr); err != nil {
			return err
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

func (dc *transferAwareDataConverter) WithSerializationContext(ctx SerializationContext) DataConverter {
	if _, ok := dc.parent.(DataConverterWithSerializationContext); !ok {
		return dc
	}
	result := *dc
	result.parent = WithDataConverterSerializationContext(dc.parent, ctx)
	return &result
}

func (dc *transferAwareDataConverter) WithWorkflowContext(ctx workflowcontext.Context) DataConverter {
	result := *dc
	if parent, ok := dc.parent.(ContextAware); ok {
		result.parent = parent.WithWorkflowContext(ctx)
	}
	result.context = nil
	result.workflowContext = ctx
	return &result
}

func (dc *transferAwareDataConverter) WithContext(ctx context.Context) DataConverter {
	result := *dc
	if parent, ok := dc.parent.(ContextAware); ok {
		result.parent = parent.WithContext(ctx)
	}
	result.context = ctx
	result.workflowContext = nil
	return &result
}
