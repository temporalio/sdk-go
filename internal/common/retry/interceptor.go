// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

package retry

import (
	"context"
	"math"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/backoffutils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const (
	// NoMaximumAttempts is a value that maximumAttempts can be set to if total number of attempts should be unlimited.
	NoMaximumAttempts = 0
	// NoInterval is a value that maximumInterval can be set to if there should be no upper bound on individual retry attempt delay.
	NoInterval = 0
	// DefaultBackoffCoefficient is default backOffCoefficient for retryPolicy
	DefaultBackoffCoefficient = 2.0
	// DefaultMaximumInterval is default maximum amount of time for an individual retry.
	DefaultMaximumInterval = 10 * time.Second
	// DefaultExpirationInterval is default expiration time for all retry attempts.
	DefaultExpirationInterval = time.Minute
	// DefaultMaximumAttempts is default maximum number of attempts.
	DefaultMaximumAttempts = NoMaximumAttempts
	// DefaultJitter is a default jitter applied on the backoff interval for delay randomization.
	DefaultJitter = 0.2
)

type (
	// GrpcRetryConfig defines required configuration for exponential backoff function that is supplied to gRPC retrier.
	GrpcRetryConfig struct {
		initialInterval    time.Duration
		backoffCoefficient float64
		maximumInterval    time.Duration
		expirationInterval time.Duration
		jitter             float64
		maximumAttempts    int
	}

	contextKey string
)

// SetBackoffCoefficient sets rate at which backoff coefficient will change.
func (g *GrpcRetryConfig) SetBackoffCoefficient(backoffCoefficient float64) {
	g.backoffCoefficient = backoffCoefficient
}

// SetMaximumInterval defines maximum amount of time between attempts.
func (g *GrpcRetryConfig) SetMaximumInterval(maximumInterval time.Duration) {
	g.maximumInterval = maximumInterval
}

// SetExpirationInterval defines total amount of time that can be used for all retry attempts.
// Note that this value is ignored if deadline is set on the context.
func (g *GrpcRetryConfig) SetExpirationInterval(expirationInterval time.Duration) {
	g.expirationInterval = expirationInterval
}

// SetJitter defines level of randomization for each delay interval. For example 0.2 would mex target +- 20%
func (g *GrpcRetryConfig) SetJitter(jitter float64) {
	g.jitter = jitter
}

// SetMaximumAttempts defines maximum total number of retry attempts.
func (g *GrpcRetryConfig) SetMaximumAttempts(maximumAttempts int) {
	g.maximumAttempts = maximumAttempts
}

// NewGrpcRetryConfig creates new retry config with specified initial interval and defaults for other parameters.
// Use SetXXX functions on this config in order to customize values.
func NewGrpcRetryConfig(initialInterval time.Duration) *GrpcRetryConfig {
	return &GrpcRetryConfig{
		initialInterval:    initialInterval,
		backoffCoefficient: DefaultBackoffCoefficient,
		maximumInterval:    DefaultMaximumInterval,
		expirationInterval: DefaultExpirationInterval,
		jitter:             DefaultJitter,
		maximumAttempts:    DefaultMaximumAttempts,
	}
}

const (
	// ConfigKey context key for GrpcRetryConfig
	ConfigKey = contextKey("RetryConfig")
)

// NewRetryOptionsInterceptor creates a new gRPC interceptor that populates retry options for each call based on values
// provided in the context.
func NewRetryOptionsInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if rc, ok := ctx.Value(ConfigKey).(GrpcRetryConfig); ok {
			if _, ok := ctx.Deadline(); !ok {
				deadlineCtx, cancel := context.WithDeadline(ctx, time.Now().Add(rc.expirationInterval))
				defer cancel()
				ctx = deadlineCtx
			}
			opts = append(opts, grpc_retry.WithBackoff(func(attempt uint) time.Duration {
				next := float64(rc.initialInterval) * math.Pow(rc.backoffCoefficient, float64(attempt))
				if rc.maximumInterval != NoInterval {
					next = math.Min(next, float64(rc.maximumInterval))
				}
				return backoffutils.JitterUp(time.Duration(next), rc.jitter)
			}))
			if rc.maximumAttempts != NoMaximumAttempts {
				grpc_retry.WithMax(uint(rc.maximumAttempts))
			}
			// We have to deal with plain gRPC error codes instead of service errors here as actual error translation
			// happens after invoker is called below and invoker must have correct retry options right away in order to
			// supply them to the gRPC retrier.
			// Retrying only transient error codes, following statuses are excluded: Ok, AlreadyExists, FailedPrecondition,
			// InvalidArgument, NotFound, PermissionDenied, Unauthenticated, Unimplemented.
			grpc_retry.WithCodes(codes.Aborted, codes.Canceled, codes.DataLoss, codes.DeadlineExceeded, codes.Internal,
				codes.OutOfRange, codes.ResourceExhausted, codes.Unavailable, codes.Unknown)
		} else {
			// Do not retry if retry config is not set.
			opts = append(opts, grpc_retry.Disable())
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
