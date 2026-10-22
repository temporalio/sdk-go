package converter

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// -- USER API -----------------------------------------------------------

// ValueWithTransferTypeConverter is a type that opts-in to transfer type conversion.
//
// Exposed as: [go.temporal.io/sdk/converter.ValueWithTransferTypeConverter]
type ValueWithTransferTypeConverter interface {
	TransferTypeConverter() (TransferTypeConverter, error)
}

// TransferTypeConverter is an opaque handle created by
// [NewTransferTypeConverter] or [NewSystemNexusTransferTypeConverter]. Do not embed this interface.
//
// Exposed as: [go.temporal.io/sdk/converter.TransferTypeConverter]
type TransferTypeConverter interface {
	// newTransferTypePtr returns a pointer to a zero transfer value.
	newTransferTypePtr() any
}

type normalTransferTypeConverter interface {
	TransferTypeConverter
	toTransferType(value any) (any, error)
	fromTransferType(transferTypePtr, valuePtr any) error
}

type systemNexusTransferTypeConverter interface {
	TransferTypeConverter
	toTransferType(cfg *SystemNexusContext, value any) (any, error)
	fromTransferType(cfg *SystemNexusContext, transferTypePtr, valuePtr any) error
}

// NewTransferTypeConverter builds a transfer type converter that can map
// Model values into Transfer values and back.
//
// Exposed as: [go.temporal.io/sdk/converter.NewTransferTypeConverter]
func NewTransferTypeConverter[Model ValueWithTransferTypeConverter, Transfer any](
	toTransferType func(*Model) (*Transfer, error),
	fromTransferType func(*Transfer, *Model) error,
) (TransferTypeConverter, error) {
	if err := validateTransferTypeTypes[Model, Transfer](); err != nil {
		return nil, err
	}
	return &transferTypeConverterImpl[Model, Transfer]{
		toTransferTypeFn:   toTransferType,
		fromTransferTypeFn: fromTransferType,
	}, nil
}

func validateTransferTypeTypes[Model, Transfer any]() error {
	modelType := reflect.TypeFor[Model]()
	if modelType.Kind() == reflect.Pointer {
		return fmt.Errorf("transfer type converter: model type must not be a pointer, got %v", modelType)
	}
	transferType := reflect.TypeFor[Transfer]()
	if transferType.Kind() == reflect.Pointer {
		return fmt.Errorf("transfer type converter: transfer type must not be a pointer, got %v", transferType)
	}
	return nil
}

type transferTypeConverterImpl[ModelType, TransferType any] struct {
	toTransferTypeFn   func(*ModelType) (*TransferType, error)
	fromTransferTypeFn func(*TransferType, *ModelType) error
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

func (tc *transferTypeConverterImpl[ModelType, TransferType]) toTransferType(value any) (any, error) {
	valuePtr, err := modelTypePtr[ModelType](value)
	if err != nil {
		return nil, err
	}
	return tc.toTransferTypeFn(valuePtr)
}

func (tc *transferTypeConverterImpl[ModelType, TransferType]) fromTransferType(transferTypePtr any, valuePtr any) error {
	v, ok := valuePtr.(*ModelType)
	if !ok {
		return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr)
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr)
	}
	return tc.fromTransferTypeFn(tvp, v)
}

type SystemNexusContext struct {
	DataConverter DataConverter
}

// NewSystemNexusTransferTypeConverter is a version of [NewTransferTypeConverter]
// whose callbacks accept a non-nil [SystemNexusContext].
//
// NOTE: Experimental.
func NewSystemNexusTransferTypeConverter[Model ValueWithTransferTypeConverter, Transfer any](
	toTransferType func(*SystemNexusContext, *Model) (*Transfer, error),
	fromTransferType func(*SystemNexusContext, *Transfer, *Model) error,
) (TransferTypeConverter, error) {
	if err := validateTransferTypeTypes[Model, Transfer](); err != nil {
		return nil, err
	}
	return &systemNexusTransferTypeConverterImpl[Model, Transfer]{
		toTransferTypeFn:   toTransferType,
		fromTransferTypeFn: fromTransferType,
	}, nil
}

type systemNexusTransferTypeConverterImpl[ModelType, TransferType any] struct {
	toTransferTypeFn   func(*SystemNexusContext, *ModelType) (*TransferType, error)
	fromTransferTypeFn func(*SystemNexusContext, *TransferType, *ModelType) error
}

func (tc *systemNexusTransferTypeConverterImpl[ModelType, TransferType]) newTransferTypePtr() any {
	return new(TransferType)
}

func (tc *systemNexusTransferTypeConverterImpl[ModelType, TransferType]) toTransferType(cfg *SystemNexusContext, value any) (any, error) {
	if cfg == nil {
		return nil, fmt.Errorf("system Nexus transfer type converter: cannot encode %T without a system Nexus config", value)
	}
	valuePtr, err := modelTypePtr[ModelType](value)
	if err != nil {
		return nil, err
	}
	return tc.toTransferTypeFn(cfg, valuePtr)
}

func (tc *systemNexusTransferTypeConverterImpl[ModelType, TransferType]) fromTransferType(cfg *SystemNexusContext, transferTypePtr any, valuePtr any) error {
	if cfg == nil {
		return fmt.Errorf("system Nexus transfer type converter: cannot decode into %T without a system Nexus config", valuePtr)
	}
	v, ok := valuePtr.(*ModelType)
	if !ok {
		return fmt.Errorf("transfer type converter: want value of type %T, got %T", (*ModelType)(nil), valuePtr)
	}
	tvp, ok := transferTypePtr.(*TransferType)
	if !ok {
		return fmt.Errorf("transfer type converter: want transfer value of type %T, got %T", (*TransferType)(nil), transferTypePtr)
	}
	return tc.fromTransferTypeFn(cfg, tvp, v)
}

func encodeTransferType(tc TransferTypeConverter, cfg *SystemNexusContext, value any) (any, error) {
	switch tc := tc.(type) {
	case normalTransferTypeConverter:
		return tc.toTransferType(value)
	case systemNexusTransferTypeConverter:
		return tc.toTransferType(cfg, value)
	default:
		return nil, fmt.Errorf("transfer type converter: unsupported implementation %T", tc)
	}
}

func decodeTransferType(tc TransferTypeConverter, cfg *SystemNexusContext, transferTypePtr, valuePtr any) error {
	switch tc := tc.(type) {
	case normalTransferTypeConverter:
		return tc.fromTransferType(transferTypePtr, valuePtr)
	case systemNexusTransferTypeConverter:
		return tc.fromTransferType(cfg, transferTypePtr, valuePtr)
	default:
		return fmt.Errorf("transfer type converter: unsupported implementation %T", tc)
	}
}

// -- DATA CONVERTERS ----------------------------------------------------------

// transferAwareDataConverter wraps a parent data converter and applies
// transfer type conversion to values that implement
// [ValueWithTransferTypeConverter].
type transferAwareDataConverter struct {
	parent DataConverter
	// systemNexus configures callbacks created by [NewSystemNexusTransferTypeConverter].
	systemNexus *SystemNexusContext
	// transferTypeConverters is a cache that maps transfer-convertible types
	// to their transfer type converters.
	transferTypeConverters *sync.Map
}

var _ DataConverter = (*transferAwareDataConverter)(nil)
var _ DataConverterWithSerializationContext = (*transferAwareDataConverter)(nil)
var _ ContextAware = (*transferAwareDataConverter)(nil)

// MakeTransferAware wraps a non-nil dc to support transfer type conversion.
// If dc is already a transfer-aware wrapper, it returns dc unchanged.
//
// NOTE: Experimental.
func MakeTransferAware(dc DataConverter) DataConverter {
	if tadc, ok := dc.(*transferAwareDataConverter); ok {
		return tadc
	}
	return &transferAwareDataConverter{
		parent:                 dc,
		transferTypeConverters: new(sync.Map),
	}
}

func (dc *transferAwareDataConverter) markSystemNexusPayload(payload *commonpb.Payload) {
	if dc.systemNexus == nil || payload == nil {
		return
	}
	payload.Metadata[proxy.SystemPayloadMetadataKey] = []byte("true")
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
	payload, err := dc.parent.ToPayload(transferType)
	if err != nil {
		return payload, err
	}
	dc.markSystemNexusPayload(payload)
	return payload, nil
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
	payloads, err := dc.parent.ToPayloads(transferTypes...)
	if err != nil {
		return payloads, err
	}
	if dc.systemNexus != nil {
		for _, payload := range payloads.GetPayloads() {
			dc.markSystemNexusPayload(payload)
		}
	}
	return payloads, nil
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
	transferType, err := encodeTransferType(tc, dc.systemNexus, value)
	return transferType, true, err
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
	return decodeTransferType(tc, dc.systemNexus, transferTypePtr, valuePtr)
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
		if err := decodeTransferType(tc, dc.systemNexus, transferTypePtrs[i], valuePtr); err != nil {
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
	parent, ok := dc.parent.(ContextAware)
	if !ok {
		return dc
	}
	result := *dc
	result.parent = parent.WithWorkflowContext(ctx)
	return &result
}

func (dc *transferAwareDataConverter) WithContext(ctx context.Context) DataConverter {
	parent, ok := dc.parent.(ContextAware)
	if !ok {
		return dc
	}
	result := *dc
	result.parent = parent.WithContext(ctx)
	return &result
}

func WithDataConverterSystemNexusContext(dc, targetDC DataConverter) DataConverter {
	result := *dc.(*transferAwareDataConverter)
	result.systemNexus = &SystemNexusContext{DataConverter: targetDC}
	return &result
}
