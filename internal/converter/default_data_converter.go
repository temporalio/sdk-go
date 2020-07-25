package converter

var (
	// DefaultDataConverter is default data converter used by Temporal worker.
	DefaultDataConverter = NewCompositeDataConverter(
		NewNilPayloadConverter(),
		NewByteSlicePayloadConverter(),
		// Only one proto converter should be used.
		// Although they check for different interfaces (proto.Message and proto.Marshaler) all proto messages implements both interfaces.
		NewProtoJSONPayloadConverter(),
		// NewProtoPayloadConverter(),
		NewJSONPayloadConverter(),
	)
)
