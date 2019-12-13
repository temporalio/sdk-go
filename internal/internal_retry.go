// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

// All code in this file is private to the package.

import (
	"context"
	"time"

	"go.uber.org/yarpc/yarpcerrors"

	s "go.temporal.io/temporal/.gen/go/shared"
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
	case *s.BadRequestError,
		*s.EntityNotExistsError,
		*s.WorkflowExecutionAlreadyStartedError,
		*s.DomainAlreadyExistsError,
		*s.QueryFailedError,
		*s.DomainNotActiveError,
		*s.CancellationAlreadyRequestedError:
		return false
	}

	if err == errShutdown {
		return false
	}

	// s.InternalServiceError
	// s.ServiceBusyError
	// s.LimitExceededError
	return true
}

func isServiceTransientErrorGRPC(err error) bool {
	// Retrying by default so it covers all transport errors.
	st := yarpcerrors.FromError(err)
	switch st.Code() {
	case yarpcerrors.CodeInvalidArgument,
		yarpcerrors.CodeNotFound,
		yarpcerrors.CodeAlreadyExists:
		return false
	}

	if err == errShutdown {
		return false
	}

	// s.InternalServiceError
	// s.ServiceBusyError
	// s.LimitExceededError
	return true
}
