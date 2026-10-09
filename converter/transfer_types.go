package converter

import commonconverter "go.temporal.io/sdk/internal/common/converter"

// ValueWithTransferTypeConverter is a type that opts in to transfer type conversion.
// Construct its converter with [NewTransferTypeConverter]. The method must be pure,
// safe for concurrent calls, and implemented with a value receiver. Its result is
// cached by concrete model type and must not depend on the receiver's state.
//
// Transfer conversion applies only to top-level payload values. For a non-pointer
// model T, encode T or a non-nil *T and decode into a non-nil *T. Workflow and
// activity parameters must use T rather than *T. Nil model pointers, **T decoding,
// and inheriting the method through embedding are unsupported.
//
// NOTE: Experimental.
type ValueWithTransferTypeConverter = commonconverter.ValueWithTransferTypeConverter

// TransferTypeConverter is an opaque handle created by [NewTransferTypeConverter].
// Do not embed this interface.
//
// NOTE: Experimental.
type TransferTypeConverter = commonconverter.TransferTypeConverter

// NewTransferTypeConverter builds a converter between Model and Transfer values.
// Callbacks receive non-nil pointers and must be pure, thread-safe, command-free,
// and replay-stable. Encoding must return a non-nil transfer pointer on success.
// Model and Transfer must not be pointer types.
//
// NOTE: Experimental.
func NewTransferTypeConverter[Model ValueWithTransferTypeConverter, Transfer any](
	toTransferType func(*Model) (*Transfer, error),
	fromTransferType func(*Transfer, *Model) error,
) (TransferTypeConverter, error) {
	return commonconverter.NewTransferTypeConverter(toTransferType, fromTransferType)
}

// MakeTransferAware wraps dc to support transfer type conversion. A nil dc uses
// [GetDefaultDataConverter]; an already transfer-aware converter is unchanged.
//
// NOTE: Experimental.
func MakeTransferAware(dc DataConverter) DataConverter {
	if dc == nil {
		return GetDefaultDataConverter()
	}
	return commonconverter.MakeTransferAware(dc)
}
