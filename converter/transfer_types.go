package converter

import commonconverter "go.temporal.io/sdk/internal/common/converter"

// ValueWithTransferTypeConverter is a type that opts-in to transfer type conversion.
// To encode a ValueWithTransferTypeConverter value as a payload, the SDK encodes
// the value using its transfer type converter first and then passes the
// resulting "transfer value" to the data converter. To decode a
// ValueWithTransferTypeConverter, the SDK reads a transfer value from the data
// converter and decodes it with the transfer type converter.
//
// TransferTypeConverter returns a value's transfer type converter.
// Create one with [NewTransferTypeConverter].
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
type ValueWithTransferTypeConverter = commonconverter.ValueWithTransferTypeConverter

// TransferTypeConverter is an opaque handle created by
// [NewTransferTypeConverter]. Do not embed this interface.
//
// NOTE: Experimental.
type TransferTypeConverter = commonconverter.TransferTypeConverter

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
	return commonconverter.NewTransferTypeConverter(toTransferType, fromTransferType)
}
