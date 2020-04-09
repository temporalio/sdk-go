package interceptors

import (
	"go.temporal.io/temporal/internal"
)

type (
	// WorkflowInterceptorFactory is used to create a single link in the interceptor chain
	WorkflowInterceptorFactory = internal.WorkflowInterceptorFactory

	// WorkflowInterceptor is an interface that can be implemented to intercept calls to the workflow function
	// as well calls done by the workflow code.
	// Use worker.WorkflowInterceptorBase as a base struct for implementations that do not want to implement every method.
	// Interceptor implementation must forward calls to the next in the interceptor chain.
	// All code in the interceptor is executed in the workflow.Context of a workflow. So all the rules and restrictions
	// that apply to the workflow code should be obeyed by the interceptor implementation.
	// Use workflow.IsReplaying(ctx) to filter out duplicated calls.
	WorkflowInterceptor = internal.WorkflowInterceptor

	// WorkflowInterceptorBase is a noop implementation of WorkflowInterceptor that just forwards requests
	// to the next link in an interceptor chain. To be used as base implementation of interceptors.
	WorkflowInterceptorBase = internal.WorkflowInterceptorBase
)
