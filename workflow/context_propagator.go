package workflow

import "go.temporal.io/sdk/internal"

type (
	// HeaderReader is an interface to read information from temporal headers
	HeaderReader = internal.HeaderReader

	// HeaderWriter is an interface to write information to temporal headers
	HeaderWriter = internal.HeaderWriter

	// ContextPropagator is an interface that determines what information from
	// context to pass along
	ContextPropagator = internal.ContextPropagator

	// CodecContextPropagator optionally uses the configured client's payload
	// codec for this propagator's headers. Existing propagators default to false.
	CodecContextPropagator = internal.CodecContextPropagator
)
