package retry

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/stretchr/testify/require"
	errordetailspb "go.temporal.io/api/errordetails/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestRetryInfoDelayFloor(t *testing.T) {
	retryInfo := func(d time.Duration) *errdetails.RetryInfo {
		return &errdetails.RetryInfo{RetryDelay: durationpb.New(d)}
	}
	tests := []struct {
		name     string
		err      error
		wantWait time.Duration
	}{
		{
			name:     "RetryInfo raises capped backoff",
			err:      resourceExhaustedWithDetails(t, retryInfo(25*time.Millisecond)),
			wantWait: 25 * time.Millisecond,
		},
		{
			name:     "largest duplicate RetryInfo is used",
			err:      resourceExhaustedWithDetails(t, retryInfo(15*time.Millisecond), retryInfo(30*time.Millisecond)),
			wantWait: 30 * time.Millisecond,
		},
		{
			name:     "longer client backoff is retained",
			err:      resourceExhaustedWithDetails(t, retryInfo(5*time.Millisecond)),
			wantWait: 10 * time.Millisecond,
		},
		{
			name:     "RetryInfo on other status is ignored",
			err:      errorWithDetails(t, codes.Unavailable, retryInfo(25*time.Millisecond)),
			wantWait: 10 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.WithValue(t.Context(), ConfigKey, testGrpcRetryConfig(2))
				attempts := invokeWithGrpcRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, tt.err, nil)
				require.Len(t, attempts, 2)
				require.Equal(t, tt.wantWait, attempts[1].Sub(attempts[0]))
			})
		})
	}
}

func TestRetryInfoDelayFloorResetsBetweenResponsesAndCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		interceptor := NewRetryOptionsInterceptor(&atomic.Bool{})
		withDelay := resourceExhaustedWithDetails(t, &errdetails.RetryInfo{RetryDelay: durationpb.New(100 * time.Millisecond)})
		withoutDelay := resourceExhaustedWithDetails(t)

		ctx := context.WithValue(t.Context(), ConfigKey, testGrpcRetryConfig(3))
		attempts := invokeWithGrpcRetry(interceptor, ctx, withDelay, withoutDelay, nil)
		require.Len(t, attempts, 3)
		require.Equal(t, 100*time.Millisecond, attempts[1].Sub(attempts[0]))
		require.Equal(t, 10*time.Millisecond, attempts[2].Sub(attempts[1]))

		// A floor left over from the previous call must not leak into this one.
		ctx = context.WithValue(t.Context(), ConfigKey, testGrpcRetryConfig(2))
		attempts = invokeWithGrpcRetry(interceptor, ctx, withoutDelay, nil)
		require.Len(t, attempts, 2)
		require.Equal(t, 10*time.Millisecond, attempts[1].Sub(attempts[0]))
	})
}

func testGrpcRetryConfig(maxAttempts int) *GrpcRetryConfig {
	config := NewGrpcRetryConfig(10 * time.Millisecond)
	config.SetBackoffCoefficient(1)
	config.SetMaximumInterval(10 * time.Millisecond)
	config.SetJitter(0)
	config.SetMaximumAttempts(maxAttempts)
	return config
}

// invokeWithGrpcRetry runs the interceptor on top of the grpc retry interceptor, returning each attempt's start time.
// Attempt N returns responses[N-1], or nil once responses run out.
func invokeWithGrpcRetry(interceptor grpc.UnaryClientInterceptor, ctx context.Context, responses ...error) []time.Time {
	retryInterceptor := grpc_retry.UnaryClientInterceptor()
	var attempts []time.Time
	_ = interceptor(ctx, "test-method", nil, nil, nil,
		func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			return retryInterceptor(ctx, method, req, reply, cc,
				func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
					attempts = append(attempts, time.Now())
					if len(attempts) <= len(responses) {
						return responses[len(attempts)-1]
					}
					return nil
				}, opts...)
		},
	)
	return attempts
}

func resourceExhaustedWithDetails(t *testing.T, details ...protoadapt.MessageV1) error {
	t.Helper()
	return errorWithDetails(t, codes.ResourceExhausted, details...)
}

func errorWithDetails(t *testing.T, code codes.Code, details ...protoadapt.MessageV1) error {
	t.Helper()
	st, err := status.New(code, "test error").WithDetails(details...)
	require.NoError(t, err)
	return st.Err()
}

func TestIsWorkflowTaskCompletionBufferLost(t *testing.T) {
	st, err := status.New(codes.Aborted, "buffer lost").WithDetails(&errordetailspb.WorkflowTaskCompletionBufferLostFailure{})
	require.NoError(t, err)
	bufferLostErr := st.Err()

	require.True(t, IsWorkflowTaskCompletionBufferLost(bufferLostErr))
	// The client's errorInterceptor converts the server status into a typed serviceerror before the
	// resend loop sees it, so detection must handle that form, not just the raw status.
	require.True(t, IsWorkflowTaskCompletionBufferLost(serviceerror.FromStatus(st)))
	require.False(t, IsWorkflowTaskCompletionBufferLost(serviceerror.NewUnavailable("nope")))
	// A plain Aborted without the detail is not buffer loss.
	require.False(t, IsWorkflowTaskCompletionBufferLost(status.Error(codes.Aborted, "other")))
	require.False(t, IsWorkflowTaskCompletionBufferLost(nil))

	// Buffer loss must not be retried at the gRPC layer, though plain Aborted still is.
	require.False(t, IsRetryable(bufferLostErr, &atomic.Bool{}))
	require.True(t, IsRetryable(status.Error(codes.Aborted, "other"), &atomic.Bool{}))
}
