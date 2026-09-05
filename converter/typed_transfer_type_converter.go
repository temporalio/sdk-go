package converter

import (
	"fmt"
	"reflect"
)

// NewTypedTransferTypeConverter creates a [TransferTypeConverter] from typed
// conversion functions.
//
// The toTransferType function receives an application value and returns the
// transfer value passed to the configured [DataConverter]. The converter also
// accepts a pointer to Value and dereferences it before calling toTransferType,
// allowing a value-receiver [TransferTypeConvertible] implementation to handle
// both Value and *Value sources.
//
// The fromTransferType function receives the decoded transfer value and returns
// the application value. For a non-pointer Transfer type, the converter decodes
// into *Transfer and dereferences it before calling fromTransferType. For a
// pointer Transfer type, such as a protocol buffer message, the converter
// creates and passes a fresh non-nil Transfer value directly.
//
// The functions can be called concurrently and during workflow execution. They
// must be safe for concurrent use and must be deterministic, fast, and
// nonblocking. A nil function causes the corresponding conversion to fail.
//
// NOTE: Experimental.
func NewTypedTransferTypeConverter[Value, Transfer any](
	toTransferType func(Value) (Transfer, error),
	fromTransferType func(Transfer) (Value, error),
) TransferTypeConverter {
	return &typedTransferTypeConverter[Value, Transfer]{
		toTransferType:   toTransferType,
		fromTransferType: fromTransferType,
	}
}

type typedTransferTypeConverter[Value, Transfer any] struct {
	toTransferType   func(Value) (Transfer, error)
	fromTransferType func(Transfer) (Value, error)
}

func (*typedTransferTypeConverter[Value, Transfer]) NewTransferType() any {
	transferType := reflect.TypeFor[Transfer]()
	if transferType.Kind() != reflect.Pointer {
		return new(Transfer)
	}

	transferValue := reflect.New(transferType.Elem())
	if transferValue.Type() != transferType {
		transferValue = transferValue.Convert(transferType)
	}
	return transferValue.Interface()
}

func (c *typedTransferTypeConverter[Value, Transfer]) ToTransferType(value any) (any, error) {
	if c.toTransferType == nil {
		return nil, fmt.Errorf("typed transfer type converter has nil to-transfer function")
	}

	typedValue, ok := value.(Value)
	if !ok {
		valuePtr, pointerOK := value.(*Value)
		if !pointerOK || valuePtr == nil {
			valueType := reflect.TypeFor[Value]()
			return nil, fmt.Errorf(
				"typed transfer type converter expected application value %v or %v, got %T",
				valueType,
				reflect.PointerTo(valueType),
				value,
			)
		}
		typedValue = *valuePtr
	}
	return c.toTransferType(typedValue)
}

func (c *typedTransferTypeConverter[Value, Transfer]) FromTransferType(value any) (any, error) {
	if c.fromTransferType == nil {
		return nil, fmt.Errorf("typed transfer type converter has nil from-transfer function")
	}

	transferType := reflect.TypeFor[Transfer]()
	if transferType.Kind() == reflect.Pointer {
		transferValue, ok := value.(Transfer)
		if !ok {
			return nil, fmt.Errorf(
				"typed transfer type converter expected transfer value %v, got %T",
				transferType,
				value,
			)
		}
		return c.fromTransferType(transferValue)
	}

	transferValuePtr, ok := value.(*Transfer)
	if !ok {
		return nil, fmt.Errorf(
			"typed transfer type converter expected transfer value %v, got %T",
			reflect.PointerTo(transferType),
			value,
		)
	}
	return c.fromTransferType(*transferValuePtr)
}
