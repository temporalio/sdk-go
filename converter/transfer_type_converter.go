package converter

// TransferTypeConvertible identifies an application value that uses another Go
// type as its representation for payload conversion.
//
// The SDK looks for this interface only at the top level: on the source
// application value when encoding and on the requested application destination
// when decoding. It does not recursively convert fields, collection elements,
// or the transfer value itself. The configured [DataConverter] remains
// responsible for serializing the transfer value.
//
// The TransferTypeConverter method must return a non-nil converter with
// equivalent behavior for every value of the marked Go type. The returned
// converter does not need to be the same instance. The SDK can call the method
// concurrently and can obtain the converter from a value other than the one
// being encoded or decoded.
//
// Normal Go method-set rules determine which values opt in. Defining
// TransferTypeConverter with a value receiver on T opts in both T and *T;
// defining it with a pointer receiver opts in only *T.
//
// TransferTypeConverter and the returned converter's methods can run during
// workflow execution. Implementations must be safe for concurrent use and must
// be deterministic, fast, and nonblocking.
//
// The application-to-transfer mapping is a payload and workflow-history
// compatibility contract. Adding, removing, or changing a mapping can make
// existing payloads or workflow histories incompatible and can affect replay.
//
// NOTE: Experimental.
type TransferTypeConvertible interface {
	// TransferTypeConverter returns the converter for the marked Go type.
	TransferTypeConverter() TransferTypeConverter
}

// TransferTypeConverter converts between an application value and the value
// passed to the configured [DataConverter].
//
// Its methods can be called concurrently and during workflow execution.
// Implementations must be safe for concurrent use and must be deterministic,
// fast, and nonblocking. A non-nil error from ToTransferType or
// FromTransferType stops payload conversion and is returned to the caller,
// possibly wrapped with additional conversion context.
//
// NOTE: Experimental.
type TransferTypeConverter interface {
	// NewTransferType returns a fresh, non-nil pointer into which the configured
	// DataConverter can decode a transfer value. Each call must return a new
	// decode destination that is not shared with any other call. The pointer must
	// be suitable as a DataConverter decode destination. Returning nil, a typed
	// nil pointer, or a non-pointer causes payload decoding to fail.
	//
	// After decoding, the SDK passes this exact pointer, without dereferencing or
	// copying it, to FromTransferType.
	NewTransferType() any

	// ToTransferType converts a top-level application value before payload
	// encoding. The value is the original value and can be either T or
	// *T when both implement TransferTypeConvertible. Untyped nil values and
	// typed nil pointers bypass transfer conversion.
	//
	// The returned transfer value, including nil, is passed directly to the
	// configured DataConverter and is not transfer-converted again. A non-nil
	// error stops conversion and is propagated to the caller, possibly wrapped
	// with additional context.
	ToTransferType(value any) (any, error)

	// FromTransferType reconstructs an application value from a decoded transfer
	// value. The value is the exact pointer returned by NewTransferType after the
	// configured DataConverter has decoded into it.
	//
	// The returned application value must be directly assignable to the element
	// type of the decode destination originally supplied by the caller. The SDK
	// does not dereference or otherwise coerce the result. A nil result is valid
	// only when that destination element type can represent nil. A non-nil error
	// stops conversion and is propagated to the caller, possibly wrapped with
	// additional context.
	FromTransferType(value any) (any, error)
}
