package converter

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
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
// Create one with [NewTransferTypeConverter] or [NewSystemNexusTransferTypeConverter].
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
// [NewTransferTypeConverter] or [NewSystemNexusTransferTypeConverter]. Do not embed this interface.
//
// NOTE: Experimental.
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
	toTransferType(cfg *SystemNexusConfig, value any) (any, error)
	fromTransferType(cfg *SystemNexusConfig, transferTypePtr, valuePtr any) error
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

type SystemNexusConfig struct {
	DataConverter DataConverter
}

// NewSystemNexusTransferTypeConverter is a version of [NewTransferTypeConverter]
// whose callbacks accept a non-nil [SystemNexusConfig].
//
// NOTE: Experimental.
func NewSystemNexusTransferTypeConverter[Model ValueWithTransferTypeConverter, Transfer any](
	toTransferType func(*SystemNexusConfig, *Model) (*Transfer, error),
	fromTransferType func(*SystemNexusConfig, *Transfer, *Model) error,
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
	toTransferTypeFn   func(*SystemNexusConfig, *ModelType) (*TransferType, error)
	fromTransferTypeFn func(*SystemNexusConfig, *TransferType, *ModelType) error
}

func (tc *systemNexusTransferTypeConverterImpl[ModelType, TransferType]) newTransferTypePtr() any {
	return new(TransferType)
}

func (tc *systemNexusTransferTypeConverterImpl[ModelType, TransferType]) toTransferType(cfg *SystemNexusConfig, value any) (any, error) {
	if cfg == nil {
		return nil, fmt.Errorf("system Nexus transfer type converter: cannot encode %T without a system Nexus config", value)
	}
	valuePtr, err := modelTypePtr[ModelType](value)
	if err != nil {
		return nil, err
	}
	return tc.toTransferTypeFn(cfg, valuePtr)
}

func (tc *systemNexusTransferTypeConverterImpl[ModelType, TransferType]) fromTransferType(cfg *SystemNexusConfig, transferTypePtr any, valuePtr any) error {
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

func encodeTransferType(tc TransferTypeConverter, cfg *SystemNexusConfig, value any) (any, error) {
	switch tc := tc.(type) {
	case normalTransferTypeConverter:
		return tc.toTransferType(value)
	case systemNexusTransferTypeConverter:
		return tc.toTransferType(cfg, value)
	default:
		return nil, fmt.Errorf("transfer type converter: unsupported implementation %T", tc)
	}
}

func decodeTransferType(tc TransferTypeConverter, cfg *SystemNexusConfig, transferTypePtr, valuePtr any) error {
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
	systemNexus *SystemNexusConfig
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

func (dc *transferAwareDataConverter) markSystemNexusPayload(value any, payload *commonpb.Payload) {
	if dc.systemNexus == nil || payload == nil {
		return
	}
	// RawValue bypasses conversion and returns a caller-owned payload unchanged.
	if raw, ok := value.(interface{ Payload() *commonpb.Payload }); ok && raw.Payload() == payload {
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
	dc.markSystemNexusPayload(transferType, payload)
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
		for i, payload := range payloads.GetPayloads() {
			dc.markSystemNexusPayload(transferTypes[i], payload)
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

func (dc *transferAwareDataConverter) WithWorkflowContext(ctx WorkflowContext) DataConverter {
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

func WithSystemNexusDataConverter(dc, targetDC DataConverter) DataConverter {
	result := *dc.(*transferAwareDataConverter)
	result.systemNexus = &SystemNexusConfig{DataConverter: targetDC}
	return &result
}
