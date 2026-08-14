package converter

import (
	"fmt"
	"reflect"

	commonpb "go.temporal.io/api/common/v1"
)

// TransferTypeConvertible is implemented by values that use a separate transfer
// representation when serialized by Temporal.
//
// This API is experimental and may change in a future release.
type TransferTypeConvertible interface {
	TransferTypeConverter() TransferTypeConverter
}

// TransferTypeConverter converts values between a user-facing type and its
// transfer representation.
//
// This API is experimental and may change in a future release.
type TransferTypeConverter interface {
	// NewTransferType returns a fresh pointer that the configured data converter
	// populates before it is passed to FromTransferType.
	NewTransferType() any
	// ToTransferType converts a user-facing value before payload conversion.
	ToTransferType(value any) (any, error)
	// FromTransferType converts the populated transfer type after payload conversion.
	FromTransferType(value any) (any, error)
}

// WrapDataConverter enables transfer type conversion for a data converter.
// SDK clients, workers, and workflow contexts apply this automatically.
//
// This API is experimental and may change in a future release.
func WrapDataConverter(dc DataConverter) DataConverter {
	if dc == nil {
		return nil
	}
	if _, ok := dc.(*transferTypeDataConverter); ok {
		return dc
	}
	return &transferTypeDataConverter{parent: dc}
}

type transferTypeDataConverter struct{ parent DataConverter }

// UnderlyingDataConverter returns the converter wrapped for transfer type support.
func (dc *transferTypeDataConverter) UnderlyingDataConverter() DataConverter { return dc.parent }

func (dc *transferTypeDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	value, err := dc.toTransferType(value)
	if err != nil {
		return nil, err
	}
	return dc.parent.ToPayload(value)
}

func (dc *transferTypeDataConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	converted := make([]any, len(values))
	for i, value := range values {
		var err error
		converted[i], err = dc.toTransferType(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}
	}
	return dc.parent.ToPayloads(converted...)
}

func (dc *transferTypeDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return dc.parent.FromPayload(nil, valuePtr)
	}
	return dc.FromPayloads(&commonpb.Payloads{Payloads: []*commonpb.Payload{payload}}, valuePtr)
}

func (dc *transferTypeDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return dc.parent.FromPayloads(nil, valuePtrs...)
	}
	innerPtrs := append([]any(nil), valuePtrs...)
	converters := make([]TransferTypeConverter, len(valuePtrs))
	for i := 0; i < len(payloads.Payloads) && i < len(valuePtrs); i++ {
		if payloads.Payloads[i] == nil {
			continue
		}
		convertible, ok := transferTypeConvertibleForDecode(valuePtrs[i])
		if !ok {
			continue
		}
		converter := convertible.TransferTypeConverter()
		if converter == nil {
			return fmt.Errorf("payload item %d: transfer type converter is nil", i)
		}
		innerPtrs[i] = converter.NewTransferType()
		if !isNonNilPointer(innerPtrs[i]) {
			return fmt.Errorf("payload item %d: transfer type converter returned invalid decode target %T", i, innerPtrs[i])
		}
		converters[i] = converter
	}
	if err := dc.parent.FromPayloads(payloads, innerPtrs...); err != nil {
		return err
	}
	for i, converter := range converters {
		if converter == nil {
			continue
		}
		value, err := converter.FromTransferType(innerPtrs[i])
		if err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
		if err := assignTransferValue(valuePtrs[i], value); err != nil {
			return fmt.Errorf("payload item %d: %w", i, err)
		}
	}
	return nil
}

func transferTypeConvertibleForDecode(valuePtr any) (TransferTypeConvertible, bool) {
	if convertible, ok := valuePtr.(TransferTypeConvertible); ok {
		return convertible, true
	}
	valueType := reflect.TypeOf(valuePtr)
	if valueType == nil || valueType.Kind() != reflect.Pointer {
		return nil, false
	}
	targetType := valueType.Elem()
	if targetType.Kind() != reflect.Pointer {
		return nil, false
	}
	convertible, ok := reflect.New(targetType.Elem()).Interface().(TransferTypeConvertible)
	return convertible, ok
}

func (dc *transferTypeDataConverter) ToString(payload *commonpb.Payload) string {
	return dc.parent.ToString(payload)
}

func (dc *transferTypeDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	return dc.parent.ToStrings(payloads)
}

func (dc *transferTypeDataConverter) WithSerializationContext(ctx SerializationContext) DataConverter {
	parent := WithDataConverterSerializationContext(dc.parent, ctx)
	if parent == dc.parent {
		return dc
	}
	return WrapDataConverter(parent)
}

func (dc *transferTypeDataConverter) toTransferType(value any) (any, error) {
	convertible, ok := value.(TransferTypeConvertible)
	if !ok {
		return value, nil
	}
	converter := convertible.TransferTypeConverter()
	if converter == nil {
		return nil, fmt.Errorf("transfer type converter is nil")
	}
	return converter.ToTransferType(value)
}

func isNonNilPointer(value any) bool {
	valueOf := reflect.ValueOf(value)
	return valueOf.Kind() == reflect.Pointer && !valueOf.IsNil()
}

func assignTransferValue(dst any, value any) error {
	dstValue := reflect.ValueOf(dst)
	if dstValue.Kind() != reflect.Pointer || dstValue.IsNil() {
		return fmt.Errorf("transfer type destination %T is not a non-nil pointer", dst)
	}
	valueValue := reflect.ValueOf(value)
	if !valueValue.IsValid() {
		return fmt.Errorf("transfer type converter returned nil, cannot assign to %v", dstValue.Elem().Type())
	}
	if valueValue.Type().AssignableTo(dstValue.Elem().Type()) {
		dstValue.Elem().Set(valueValue)
		return nil
	}
	if valueValue.Kind() == reflect.Pointer && !valueValue.IsNil() && valueValue.Elem().Type().AssignableTo(dstValue.Elem().Type()) {
		dstValue.Elem().Set(valueValue.Elem())
		return nil
	}
	return fmt.Errorf("transfer type converter returned %T, cannot assign to %v", value, dstValue.Elem().Type())
}
