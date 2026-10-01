package internal

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// -- USER API -----------------------------------------------------------

// TransferTypeConvertible is a type that opts-in to transfer type conversion.
// The first time the SDK encounters a value of type M that implements
// TransferTypeConvertible, the SDK will invoke the TransferTypeConverter
// method and cache the result. The cached transfer type converter will
// be used to encode all values of type M as their transfer type T before
// being forwarded to the data converter. Decoding a value of type M means
// getting a value of type T from the data converter and using the transfer
// type converter to convert it into a value of type M.
//
// Use [NewTransferTypeConverter] to create a new transfer type converter.
//
// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.TransferTypeConvertible]
type TransferTypeConvertible interface {
	TransferTypeConverter() (TransferTypeConverter, error)
}

// NOTE: Experimental.
//
// Exposed as: [go.temporal.io/sdk/workflow.TransferTypeConverter]
type TransferTypeConverter interface {
	transferTypeConverter() *transferTypeConverterImpl
}

// NewTransferTypeConverter builds a transfer type converter that can map
// Model values into Transfer values and back. The callbacks should be
// pure and produce replay-stable output.
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
	return &transferTypeConverterImpl{
		newTransferTypePtr: func() any {
			return new(Transfer)
		},
		toTransferType: func(ctx context.Context, value any) (any, error) {
			if valuePtr, ok := value.(*Model); ok {
				return toTransferType(ctx, valuePtr)
			}
			if value, ok := value.(Model); ok {
				return toTransferType(ctx, &value)
			}
			var zero Model
			return nil, fmt.Errorf("transfer type converter: want value of type %T or %T, got %T", zero, (*Model)(nil), value)
		},
		fromTransferType: func(ctx context.Context, transferTypePtr any, valuePtr any) error {
			v, ok := valuePtr.(*Model)
			if !ok {
				return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*Model)(nil), valuePtr)
			}
			tvp, ok := transferTypePtr.(*Transfer)
			if !ok {
				return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*Transfer)(nil), transferTypePtr)
			}
			return fromTransferType(ctx, tvp, v)
		},
		toTransferTypeWithWorkflowContext: func(ctx Context, value any) (any, error) {
			if valuePtr, ok := value.(*Model); ok {
				return toTransferTypeWithWorkflowContext(ctx, valuePtr)
			}
			if value, ok := value.(Model); ok {
				return toTransferTypeWithWorkflowContext(ctx, &value)
			}
			var zero Model
			return nil, fmt.Errorf("transfer type converter: want value of type %T or %T, got %T", zero, (*Model)(nil), value)
		},
		fromTransferTypeWithWorkflowContext: func(ctx Context, transferTypePtr any, valuePtr any) error {
			v, ok := valuePtr.(*Model)
			if !ok {
				return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*Model)(nil), valuePtr)
			}
			tvp, ok := transferTypePtr.(*Transfer)
			if !ok {
				return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*Transfer)(nil), transferTypePtr)
			}
			return fromTransferTypeWithWorkflowContext(ctx, tvp, v)
		},
	}, nil
}

type transferTypeConverterImpl struct {
	// newTransferTypePtr returns a pointer to a zero transfer value.
	newTransferTypePtr func() any

	// toTransferType converts value into its transfer value.
	toTransferType func(ctx context.Context, value any) (any, error)

	// fromTransferType reads a transfer value from transferTypePtr
	// and writes its corresponding model value into valuePtr.
	fromTransferType func(ctx context.Context, transferTypePtr any, valuePtr any) error

	// toTransferTypeWithWorkflowContext converts value into its transfer value
	// using a workflow context.
	toTransferTypeWithWorkflowContext func(ctx Context, value any) (any, error)

	// fromTransferTypeWithWorkflowContext reads a transfer value from transferTypePtr
	// and writes its corresponding model value into valuePtr using a workflow context.
	fromTransferTypeWithWorkflowContext func(ctx Context, transferTypePtr any, valuePtr any) error
}

func (tc *transferTypeConverterImpl) transferTypeConverter() *transferTypeConverterImpl {
	return tc
}

// -- DATA CONVERTERS ----------------------------------------------------------

// The default data converter, wrapped so it supports transfer type conversion.
// For values that don't implement [TransferTypeConvertible], this data
// converter behaves the same as [converter.GetDefaultDataConverter].
var defaultTransferAwareDataConverter *transferAwareDataConverter = makeTransferAware(converter.GetDefaultDataConverter())

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

// transferAwareDataConverter wraps a parent data converter and applies
// transfer type conversion to values that implement
// [TransferTypeConvertible].
type transferAwareDataConverter struct {
	parent converter.DataConverter
	// context is only set if this data converter was created by
	// [ContextAware.WithContext]. We store it so we can pass it to the
	// transfer type converter.
	context context.Context
	// workflowContext is only set if this data converter was created by
	// [ContextAware.WithWorkflowContext]. We store it so we can pass it to the
	// transfer type converter.
	workflowContext Context
	// transferTypeConverters is a cache that maps transfer-convertible types
	// to their transfer type converters.
	transferTypeConverters *sync.Map
}

var _ converter.DataConverter = (*transferAwareDataConverter)(nil)
var _ converter.DataConverterWithSerializationContext = (*transferAwareDataConverter)(nil)
var _ ContextAware = (*transferAwareDataConverter)(nil)

// transferTypeConverter fetches a converter from cache using the value's
// underlying type as a key, populating the cache if necessary.
func (dc *transferAwareDataConverter) transferTypeConverter(value TransferTypeConvertible) (*transferTypeConverterImpl, error) {
	// The value's type is either T or *T for some underlying non-pointer type T.
	// A more deeply nested pointer type like **T couldn't implement the interface.
	underlyingType := reflect.TypeOf(value)
	if underlyingType.Kind() == reflect.Pointer {
		underlyingType = underlyingType.Elem()
	}
	if tc, ok := dc.transferTypeConverters.Load(underlyingType); ok {
		return tc.(*transferTypeConverterImpl), nil
	}
	tc, err := value.TransferTypeConverter()
	if err != nil {
		return nil, fmt.Errorf("transfer type converter for %v: %w", underlyingType, err)
	}
	if tc == nil {
		return nil, fmt.Errorf("transfer type converter for %v is nil", underlyingType)
	}
	cached, _ := dc.transferTypeConverters.LoadOrStore(underlyingType, tc.transferTypeConverter())
	return cached.(*transferTypeConverterImpl), nil
}

func (dc *transferAwareDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	convertible, ok := value.(TransferTypeConvertible)
	if !ok {
		return dc.parent.ToPayload(value)
	}
	tc, err := dc.transferTypeConverter(convertible)
	if err != nil {
		return nil, err
	}
	transferType, err := dc.toTransferType(tc, value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(transferType)
}

func (dc *transferAwareDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	transferTypes := values
	copied := false
	for i, value := range values {
		convertible, ok := value.(TransferTypeConvertible)
		if !ok {
			continue
		}
		tc, err := dc.transferTypeConverter(convertible)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		transferType, err := dc.toTransferType(tc, value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		if !copied {
			transferTypes = slices.Clone(values)
			copied = true
		}
		transferTypes[i] = transferType
	}
	return dc.parent.ToPayloads(transferTypes...)
}

func (dc *transferAwareDataConverter) toTransferType(tc *transferTypeConverterImpl, value any) (any, error) {
	if dc.workflowContext != nil {
		return tc.toTransferTypeWithWorkflowContext(dc.workflowContext, value)
	}
	if dc.context != nil {
		return tc.toTransferType(dc.context, value)
	}
	return tc.toTransferType(context.Background(), value)
}

func (dc *transferAwareDataConverter) fromTransferType(tc *transferTypeConverterImpl, transferTypePtr any, valuePtr any) error {
	if dc.workflowContext != nil {
		return tc.fromTransferTypeWithWorkflowContext(dc.workflowContext, transferTypePtr, valuePtr)
	}
	if dc.context != nil {
		return tc.fromTransferType(dc.context, transferTypePtr, valuePtr)
	}
	return tc.fromTransferType(context.Background(), transferTypePtr, valuePtr)
}

// adaptTransferDecodePointer checks whether valuePtr is a non-nil **T,
// where *T implements TransferTypeConvertible. If so it returns the
// inner *T, allocating new(T) and updating *valuePtr if that pointer is nil.
// All other inputs are returned unchanged.
//
// This is useful when decoding into a pointer, e.g.
//
//   var x *MyModel
//   dc.FromPayload(payload, &x)
//
// If x is nil, we want to allocate a new MyModel and decode into it.
func adaptTransferDecodePointer(valuePtr any) any {
	argument := reflect.ValueOf(valuePtr)
	if argument.Kind() != reflect.Pointer || argument.IsNil() {
		return valuePtr
	}
	modelPtr := argument.Elem()
	if modelPtr.Kind() != reflect.Pointer ||
		!modelPtr.Type().Implements(reflect.TypeFor[TransferTypeConvertible]()) {
		return valuePtr
	}
	if modelPtr.IsNil() {
		modelPtr.Set(reflect.New(modelPtr.Type().Elem()))
	}
	return modelPtr.Interface()
}

func (dc *transferAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	valuePtr = adaptTransferDecodePointer(valuePtr)
	convertible, ok := valuePtr.(TransferTypeConvertible)
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
	type transferDestination struct {
		valuePtr  any
		converter *transferTypeConverterImpl
	}
	var destinations []transferDestination
	for i := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		valuePtr := adaptTransferDecodePointer(valuePtrs[i])
		convertible, ok := valuePtr.(TransferTypeConvertible)
		if ok {
			tc, err := dc.transferTypeConverter(convertible)
			if err != nil {
				return fmt.Errorf("transfer type converter: payload item %d: %w", i, err)
			}
			if destinations == nil {
				transferTypePtrs = slices.Clone(valuePtrs)
				destinations = make([]transferDestination, len(valuePtrs))
			}
			destinations[i].valuePtr = valuePtr
			destinations[i].converter = tc
			transferTypePtrs[i] = tc.newTransferTypePtr()
		}
	}

	if err := dc.parent.FromPayloads(payloads, transferTypePtrs...); err != nil {
		return err
	}

	for i, destination := range destinations {
		if destination.converter == nil {
			continue
		}
		if err := dc.fromTransferType(destination.converter, transferTypePtrs[i], destination.valuePtr); err != nil {
			return fmt.Errorf("transfer type converter: payload item %d: %w", i, err)
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
