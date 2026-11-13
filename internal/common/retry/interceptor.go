package retry

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/util/backoffutils"
	errordetailspb "go.temporal.io/api/errordetails/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// UnlimitedMaximumAttempts when maximum attempts is set to this special value, then the number of attempts is unlimited.
	UnlimitedMaximumAttempts = 0
	// UnlimitedInterval when maximum interval is set to this special value, then there is no upper bound on the retry delay.
	// Should not be used together with unlimited attempts as resulting retry interval can grow to unreasonable values.
	UnlimitedInterval = 0
	// DefaultBackoffCoefficient is default backOffCoefficient for retryPolicy
	DefaultBackoffCoefficient = 2.0
	// DefaultMaximumInterval is default maximum amount of time for an individual retry.
	DefaultMaximumInterval = 10 * time.Second
	// DefaultExpirationInterval is default expiration time for all retry attempts.
	DefaultExpirationInterval = time.Minute
	// DefaultMaximumAttempts is default maximum number of attempts.
	DefaultMaximumAttempts = UnlimitedMaximumAttempts
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

	GrpcMessageTooLargeError struct {
		err    error
		status *status.Status
	}

	contextKey struct{}
)

func (ck contextKey) String() string {
	return "RetryConfig"
}

// SetBackoffCoefficient sets rate at which backoff coefficient will change.
func (g *GrpcRetryConfig) SetBackoffCoefficient(backoffCoefficient float64) {
	g.backoffCoefficient = backoffCoefficient
}

// SetMaximumInterval caps the client backoff. A server RetryInfo delay can exceed this interval.
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

var (
	// ConfigKey context key for GrpcRetryConfig
	ConfigKey = contextKey{}
	// gRPC response codes that represent retryable errors.
	// The following status codes are never retried by the library:
	//    INVALID_ARGUMENT, NOT_FOUND, ALREADY_EXISTS, FAILED_PRECONDITION, ABORTED, OUT_OF_RANGE, DATA_LOSS
	// codes.DeadlineExceeded and codes.Canceled are not here (and shouldn't be here!)
	// because they are coming from go context and "context errors are not retriable based on user settings"
	// by gRPC library.
	// codes.ResourceExhausted is non-retryable if it comes from GrpcMessageTooLargeError, but otherwise is retryable.
	// codes.Internal is not included because it's retryable or non-retryable depending on server capabilities.
	retryableCodesWithoutInternal = []codes.Code{codes.Aborted, codes.ResourceExhausted, codes.Unavailable, codes.Unknown}
)

// NewRetryOptionsInterceptor retries calls when a GrpcRetryConfig is present in the context. The atomic bool is
// checked each call to determine whether internal errors are included in retry. If not present or false, internals
// are assumed to be included. A positive google.rpc.RetryInfo delay on ResourceExhausted errors floors the next wait.
func NewRetryOptionsInterceptor(excludeInternal *atomic.Bool) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		rc, ok := ctx.Value(ConfigKey).(*GrpcRetryConfig)
		if !ok {
			// Do not retry if retry config is not set.
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		if _, ok := ctx.Deadline(); !ok {
			deadlineCtx, cancel := context.WithDeadline(ctx, time.Now().Add(rc.expirationInterval))
			defer cancel()
			ctx = deadlineCtx
		}

		// Retries run serially, so each response directly determines the next delay.
		for attempt := uint(0); ; attempt++ {
			attemptCtx := ctx
			if attempt > 0 {
				md, _ := metadata.FromOutgoingContext(ctx)
				md = md.Copy()
				md.Set("x-retry-attempt", strconv.FormatUint(uint64(attempt), 10))
				attemptCtx = metadata.NewOutgoingContext(ctx, md)
			}

			err := invoker(attemptCtx, method, req, reply, cc, opts...)
			if err == nil || !IsRetryable(err, excludeInternal) {
				return err
			}
			if rc.maximumAttempts != UnlimitedMaximumAttempts && attempt+1 >= uint(rc.maximumAttempts) {
				return err
			}

			serverDelayFloor := resourceExhaustedRetryDelayFloor(err)
			if serverDelayFloor > 0 {
				if deadline, ok := ctx.Deadline(); ok && serverDelayFloor >= time.Until(deadline) {
					return err
				}
			}

			next := float64(rc.initialInterval) * math.Pow(rc.backoffCoefficient, float64(attempt+1))
			if rc.maximumInterval != UnlimitedInterval {
				next = math.Min(next, float64(rc.maximumInterval))
			}
			delay := max(backoffutils.JitterUp(time.Duration(next), rc.jitter), serverDelayFloor)
			if delay <= 0 {
				continue
			}

			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return status.FromContextError(ctx.Err()).Err()
			case <-timer.C:
			}
		}
	}
}

func resourceExhaustedRetryDelayFloor(err error) time.Duration {
	grpcStatus := status.Convert(err)
	if grpcStatus == nil || grpcStatus.Code() != codes.ResourceExhausted {
		return 0
	}

	var floor time.Duration
	for _, detail := range grpcStatus.Details() {
		retryInfo, ok := detail.(*errdetails.RetryInfo)
		if !ok || retryInfo.GetRetryDelay() == nil || retryInfo.GetRetryDelay().CheckValid() != nil {
			continue
		}
		if delay := retryInfo.GetRetryDelay().AsDuration(); delay > floor {
			floor = delay
		}
	}
	return floor
}

func IsRetryable(err error, excludeInternalFromRetry *atomic.Bool) bool {
	if _, ok := err.(*GrpcMessageTooLargeError); ok {
		return false
	}
	// Buffer loss on a paginated completion is recovered by resending every page from page 0, not by
	// retrying the single failed request, so it must never be retried at the gRPC layer.
	if IsWorkflowTaskCompletionBufferLost(err) {
		return false
	}
	grpcStatus := serviceerror.ToStatus(err)
	if grpcStatus == nil {
		return false
	}
	errCode := grpcStatus.Code()
	if slices.Contains(retryableCodesWithoutInternal, errCode) {
		return true
	}
	if errCode == codes.Internal {
		return !excludeInternalFromRetry.Load()
	}
	return false
}

// IsWorkflowTaskCompletionBufferLost reports whether err carries the server's
// WorkflowTaskCompletionBufferLostFailure detail, meaning it dropped the buffered pages of a
// paginated RespondWorkflowTaskCompleted and they must be resent from page 0.
func IsWorkflowTaskCompletionBufferLost(err error) bool {
	// serviceerror.ToStatus (not status.Convert) is required because the client's errorInterceptor has
	// already converted the error into a *serviceerror.WorkflowTaskCompletionBufferLost by the time it
	// reaches the resend loop; that type carries the detail on its Status() but not via gRPC's
	// GRPCStatus(), so status.Convert would drop it and report codes.Unknown.
	grpcStatus := serviceerror.ToStatus(err)
	if grpcStatus == nil {
		return false
	}
	for _, detail := range grpcStatus.Details() {
		if _, ok := detail.(*errordetailspb.WorkflowTaskCompletionBufferLostFailure); ok {
			return true
		}
	}
	return false
}

// GrpcMessageTooLargeErrorInterceptor checks if the error is caused by gRPC message being too large and converts it into GrpcMessageTooLargeError.
func GrpcMessageTooLargeErrorInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if grpcStatus := status.Convert(err); isGrpcMessageTooLargeStatus(grpcStatus) {
		err = &GrpcMessageTooLargeError{err: err, status: grpcStatus}
	}
	return err
}

func (e *GrpcMessageTooLargeError) Error() string {
	return e.err.Error()
}

func (e *GrpcMessageTooLargeError) Unwrap() error {
	return e.err
}

func (e *GrpcMessageTooLargeError) GRPCStatus() *status.Status {
	return e.status
}

func isGrpcMessageTooLargeStatus(status *status.Status) bool {
	if status == nil {
		return false
	}
	message := status.Message()
	// Source: https://github.com/search?q=repo%3Agrpc%2Fgrpc-go+ResourceExhausted&type=code
	return strings.HasPrefix(message, "grpc: received message larger than max") ||
		strings.HasPrefix(message, "grpc: message after decompression larger than max") ||
		strings.HasPrefix(message, "grpc: received message after decompression larger than max") ||
		strings.HasPrefix(message, "grpc: trying to send message larger than max") ||
		strings.HasPrefix(message, "grpc: message too large") ||
		strings.HasPrefix(message, "trying to send message larger than max")
}
