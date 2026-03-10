package temporal

import (
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
)

type (
	// DefaultFailureConverterOptions are optional parameters for DefaultFailureConverter creation.
	DefaultFailureConverterOptions = internal.DefaultFailureConverterOptions

	// DefaultFailureConverter seralizes errors with the option to encode common parameters under Failure.EncodedAttributes.
	DefaultFailureConverter = internal.DefaultFailureConverter
)

// NewDefaultFailureConverter creates new instance of DefaultFailureConverter.
func NewDefaultFailureConverter(opt DefaultFailureConverterOptions) *DefaultFailureConverter {
	return internal.NewDefaultFailureConverter(opt)
}

// GetDefaultDataConverter returns the default failure converter used by Temporal.
func GetDefaultFailureConverter() converter.FailureConverter {
	return internal.GetDefaultFailureConverter()
}
