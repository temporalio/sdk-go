package workflow

import (
	"go.temporal.io/sdk/internal"
)

type (
	// SagaOptions stores all saga-specific parameters inside of a context.
	SagaOptions = internal.SagaOptions

	// CompensationError implements the error interface and aggregates
	// the compensation errors that occur during execution
	CompensationError = internal.CompensationError

	// Saga implements the logic to execute compensation operations
	// https://en.wikipedia.org/wiki/Compensating_transaction that is often
	// required in Saga applications.
	//
	// Examples:
	//	 sagaOptions := &SagaOptions{
	// 		 ParallelComepensation: true,
	// 	     ContinueOnError:       true,
	// 	 }
	//   saga := workflow.NewSaga(workflow.WithSagaOptions(ctx, sagaOptions))
	//   defer func() {
	//	   if err != nil {
	//       saga.Compensate()
	//	   }
	//   }()
	//
	//  saga.AddCompensation(fooActivity, "arg1", "arg2")
	//  saga.AddCompensation(barActivity, 1, baz{})
	Saga interface {
		// Cancel cancels the current Saga.
		Cancel()

		// AddCompensation adds a compensation step to the stack.
		AddCompensation(activity interface{}, args ...interface{})

		// Compensate executes all the compensation operations in the stack.
		// After the first call, subsequent calls to a Compensate do nothing.
		Compensate() error
	}
)

// WithSagaOptions makes a copy of the context and adds the
// passed in options to the context. If saga options exists,
// it will be overwritten by the passed in value as a whole.
// So specify all the values in the options as necessary, as values
// in the existing context options will not be carried over.
func WithSagaOptions(ctx Context, options *SagaOptions) Context {
	return internal.WithSagaOptions(ctx, options)
}

// NewSaga creates a new Saga instance.
func NewSaga(ctx Context) Saga {
	return internal.NewSaga(ctx)
}
