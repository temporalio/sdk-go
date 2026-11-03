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

	// DataConverterContextPropagator optionally receives the configured data
	// converter before each ContextPropagator call.
	// The returned propagator owns conversion of its headers; other headers are
	// not converted by the SDK.
	DataConverterContextPropagator = internal.DataConverterContextPropagator
)
