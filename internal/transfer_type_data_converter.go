package internal

import (
	"context"
	"fmt"
	"reflect"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

type transferTypeDataConverter struct {
	parent converter.DataConverter
}

var _ converter.DataConverter = (*transferTypeDataConverter)(nil)
var _ converter.DataConverterWithSerializationContext = (*transferTypeDataConverter)(nil)
var _ ContextAware = (*transferTypeDataConverter)(nil)

func wrapTransferTypeDataConverter(dc converter.DataConverter) converter.DataConverter {
	if dc == nil {
		return nil
	}
	if _, ok := dc.(*transferTypeDataConverter); ok {
		return dc
	}
	return &transferTypeDataConverter{parent: dc}
}

func (dc *transferTypeDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	transferValue, converted, err := transferTypeValueForEncode(value)
	if err != nil {
		return nil, err
	}
	payload, err := dc.parent.ToPayload(transferValue)
	if err != nil && converted {
		return payload, fmt.Errorf("encode transfer value for %T: %w", value, err)
	}
	return payload, err
}

func (dc *transferTypeDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	transferValues := make([]any, len(values))
	for i, value := range values {
		transferValue, _, err := transferTypeValueForEncode(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
		transferValues[i] = transferValue
	}
	return dc.parent.ToPayloads(transferValues...)
}

func transferTypeValueForEncode(value any) (transferValue any, converted bool, err error) {
	if _, ok := value.(converter.RawValue); ok {
		return value, false, nil
	}
	if isNilPointer(value) {
		return value, false, nil
	}
	convertible, ok := value.(converter.TransferTypeConvertible)
	if !ok {
		return value, false, nil
	}
	transferConverter := convertible.TransferTypeConverter()
	if isNilValue(transferConverter) {
		return nil, false, fmt.Errorf("transfer type converter for %T is nil", value)
	}
	transferValue, err = transferConverter.ToTransferType(value)
	if err != nil {
		return nil, false, fmt.Errorf("convert %T to transfer type: %w", value, err)
	}
	return transferValue, true, nil
}

func (dc *transferTypeDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	if _, ok := valuePtr.(*converter.RawValue); ok {
		return dc.parent.FromPayload(payload, valuePtr)
	}

	parentValuePtr, prepared, err := prepareTransferTypeDecode(valuePtr)
	if err != nil {
		return err
	}
	if prepared == nil {
		return dc.parent.FromPayload(payload, valuePtr)
	}
	if err := dc.parent.FromPayload(payload, parentValuePtr); err != nil {
		return fmt.Errorf("decode transfer value for destination %T: %w", valuePtr, err)
	}
	return prepared.finish()
}

func (dc *transferTypeDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return dc.parent.FromPayloads(payloads, valuePtrs...)
	}

	parentValuePtrs := append([]any(nil), valuePtrs...)
	prepared := make([]*preparedTransferTypeDecode, len(valuePtrs))
	count := min(len(payloads.GetPayloads()), len(valuePtrs))
	for i := 0; i < count; i++ {
		if payloads.GetPayloads()[i] == nil {
			continue
		}
		if _, ok := valuePtrs[i].(*converter.RawValue); ok {
			continue
		}
		parentValuePtr, item, err := prepareTransferTypeDecode(valuePtrs[i])
		if err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
		if item != nil {
			parentValuePtrs[i] = parentValuePtr
			prepared[i] = item
		}
	}

	if err := dc.parent.FromPayloads(payloads, parentValuePtrs...); err != nil {
		return err
	}
	for i := 0; i < count; i++ {
		if prepared[i] == nil {
			continue
		}
		if err := prepared[i].finish(); err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
	}
	return nil
}

type preparedTransferTypeDecode struct {
	destination       reflect.Value
	transferValuePtr  any
	transferConverter converter.TransferTypeConverter
}

func prepareTransferTypeDecode(valuePtr any) (any, *preparedTransferTypeDecode, error) {
	convertible, destination, ok := transferTypeConvertibleForDecode(valuePtr)
	if !ok {
		return valuePtr, nil, nil
	}
	transferConverter := convertible.TransferTypeConverter()
	if isNilValue(transferConverter) {
		return nil, nil, fmt.Errorf("transfer type converter for destination %T is nil", valuePtr)
	}
	transferValuePtr := transferConverter.NewTransferType()
	if !isNonNilPointer(transferValuePtr) {
		return nil, nil, fmt.Errorf(
			"transfer type converter for destination %T returned %T from NewTransferType; want a non-nil pointer",
			valuePtr,
			transferValuePtr,
		)
	}
	return transferValuePtr, &preparedTransferTypeDecode{
		destination:       destination,
		transferValuePtr:  transferValuePtr,
		transferConverter: transferConverter,
	}, nil
}

func transferTypeConvertibleForDecode(valuePtr any) (converter.TransferTypeConvertible, reflect.Value, bool) {
	value := reflect.ValueOf(valuePtr)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil, reflect.Value{}, false
	}
	if convertible, ok := valuePtr.(converter.TransferTypeConvertible); ok {
		return convertible, value.Elem(), true
	}

	destination := value.Elem()
	if destination.Kind() != reflect.Pointer {
		return nil, reflect.Value{}, false
	}
	convertible, ok := reflect.New(destination.Type().Elem()).Interface().(converter.TransferTypeConvertible)
	if !ok {
		return nil, reflect.Value{}, false
	}
	return convertible, destination, true
}

func (decode *preparedTransferTypeDecode) finish() error {
	value, err := decode.transferConverter.FromTransferType(decode.transferValuePtr)
	if err != nil {
		return fmt.Errorf("convert transfer value to %v: %w", decode.destination.Type(), err)
	}
	if value == nil {
		if !canBeNil(decode.destination.Kind()) {
			return fmt.Errorf("transfer type converter returned nil, cannot assign to %v", decode.destination.Type())
		}
		decode.destination.Set(reflect.Zero(decode.destination.Type()))
		return nil
	}

	result := reflect.ValueOf(value)
	if !result.Type().AssignableTo(decode.destination.Type()) {
		return fmt.Errorf(
			"transfer type converter returned %T, cannot assign to %v",
			value,
			decode.destination.Type(),
		)
	}
	decode.destination.Set(result)
	return nil
}

func isNilPointer(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func isNonNilPointer(value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && !reflected.IsNil()
}

func canBeNil(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return true
	default:
		return false
	}
}

func (dc *transferTypeDataConverter) ToString(payload *commonpb.Payload) string {
	return dc.parent.ToString(payload)
}

func (dc *transferTypeDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	return dc.parent.ToStrings(payloads)
}

func (dc *transferTypeDataConverter) WithSerializationContext(
	ctx converter.SerializationContext,
) converter.DataConverter {
	return wrapTransferTypeDataConverter(
		converter.WithDataConverterSerializationContext(dc.parent, ctx),
	)
}

func (dc *transferTypeDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	return wrapTransferTypeDataConverter(WithWorkflowContext(ctx, dc.parent))
}

func (dc *transferTypeDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	return wrapTransferTypeDataConverter(WithContext(ctx, dc.parent))
}
