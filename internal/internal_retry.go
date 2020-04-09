package internal

// All code in this file is private to the package.

import (
	"context"
	"time"

	"go.temporal.io/temporal-proto/serviceerror"

	"go.temporal.io/temporal/internal/common/backoff"
)

const (
	retryServiceOperationInitialInterval    = 20 * time.Millisecond
	retryServiceOperationExpirationInterval = 60 * time.Second
	retryServiceOperationBackoff            = 1.2
)

// Creates a retry policy which allows appropriate retries for the deadline passed in as context.
// It uses the context deadline to set MaxInterval as 1/10th of context timeout
// MaxInterval = Max(context_timeout/10, 20ms)
// defaults to ExpirationInterval of 60 seconds, or uses context deadline as expiration interval
func createDynamicServiceRetryPolicy(ctx context.Context) backoff.RetryPolicy {
	timeout := retryServiceOperationExpirationInterval
	if ctx != nil {
		now := time.Now()
		if expiration, ok := ctx.Deadline(); ok && expiration.After(now) {
			timeout = expiration.Sub(now)
		}
	}
	initialInterval := retryServiceOperationInitialInterval
	maximumInterval := timeout / 10
	if maximumInterval < retryServiceOperationInitialInterval {
		maximumInterval = retryServiceOperationInitialInterval
	}

	policy := backoff.NewExponentialRetryPolicy(initialInterval)
	policy.SetBackoffCoefficient(retryServiceOperationBackoff)
	policy.SetMaximumInterval(maximumInterval)
	policy.SetExpirationInterval(timeout)
	return policy
}

func isServiceTransientError(err error) bool {
	// Retrying by default so it covers all transport errors.
	switch err.(type) {
	case *serviceerror.InvalidArgument,
		*serviceerror.NotFound,
		*serviceerror.WorkflowExecutionAlreadyStarted,
		*serviceerror.NamespaceAlreadyExists,
		*serviceerror.QueryFailed,
		*serviceerror.NamespaceNotActive,
		*serviceerror.CancellationAlreadyRequested:
		return false
	}
	return err != errShutdown
}
