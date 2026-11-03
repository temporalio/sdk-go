package retry

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	errordetailspb "go.temporal.io/api/errordetails/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
				attempts, _ := invokeWithRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, tt.err, nil)
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
		attempts, _ := invokeWithRetry(interceptor, ctx, withDelay, withoutDelay, nil)
		require.Len(t, attempts, 3)
		require.Equal(t, 100*time.Millisecond, attempts[1].Sub(attempts[0]))
		require.Equal(t, 10*time.Millisecond, attempts[2].Sub(attempts[1]))

		// A floor left over from the previous call must not leak into this one.
		ctx = context.WithValue(t.Context(), ConfigKey, testGrpcRetryConfig(2))
		attempts, _ = invokeWithRetry(interceptor, ctx, withoutDelay, nil)
		require.Len(t, attempts, 2)
		require.Equal(t, 10*time.Millisecond, attempts[1].Sub(attempts[0]))
	})
}

func TestRetryInfoDelayFloorSkipsServerJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const serverDelay = 20 * time.Millisecond
		config := testGrpcRetryConfig(2)
		config.SetJitter(0.5)
		ctx := context.WithValue(t.Context(), ConfigKey, config)
		withHint := resourceExhaustedWithDetails(t, &errdetails.RetryInfo{RetryDelay: durationpb.New(serverDelay)})

		// A positive server hint is an exact minimum. Client jitter must not move it.
		for range 20 {
			attempts, err := invokeWithRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, withHint, nil)
			require.NoError(t, err)
			require.Equal(t, serverDelay, attempts[1].Sub(attempts[0]))
		}
	})
}

func TestRetryInfoDelayFloorRespectsEffectiveDeadline(t *testing.T) {
	const serverDelay = 100 * time.Millisecond
	withHint := resourceExhaustedWithDetails(t, &errdetails.RetryInfo{RetryDelay: durationpb.New(serverDelay)})
	withoutHint := resourceExhaustedWithDetails(t)
	tests := []struct {
		name          string
		serverError   error
		deadlineAfter time.Duration
		useExplicit   bool
		wantAttempts  int
		wantStatus    codes.Code
	}{
		{name: "explicit deadline shorter than server delay", serverError: withHint, deadlineAfter: 50 * time.Millisecond, useExplicit: true, wantAttempts: 1, wantStatus: codes.ResourceExhausted},
		{name: "explicit deadline equal to server delay", serverError: withHint, deadlineAfter: serverDelay, useExplicit: true, wantAttempts: 1, wantStatus: codes.ResourceExhausted},
		{name: "explicit deadline longer than server delay", serverError: withHint, deadlineAfter: 150 * time.Millisecond, useExplicit: true, wantAttempts: 2, wantStatus: codes.OK},
		{name: "configured deadline shorter than server delay", serverError: withHint, deadlineAfter: 50 * time.Millisecond, wantAttempts: 1, wantStatus: codes.ResourceExhausted},
		{name: "configured deadline equal to server delay", serverError: withHint, deadlineAfter: serverDelay, wantAttempts: 1, wantStatus: codes.ResourceExhausted},
		{name: "configured deadline longer than server delay", serverError: withHint, deadlineAfter: 150 * time.Millisecond, wantAttempts: 2, wantStatus: codes.OK},
		{name: "no hint retains client backoff deadline behavior", serverError: withoutHint, deadlineAfter: 5 * time.Millisecond, useExplicit: true, wantAttempts: 1, wantStatus: codes.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				config := testGrpcRetryConfig(2)
				config.SetJitter(0)
				ctx := t.Context()
				if tt.useExplicit {
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, time.Now().Add(tt.deadlineAfter))
					defer cancel()
				} else {
					config.SetExpirationInterval(tt.deadlineAfter)
				}
				ctx = context.WithValue(ctx, ConfigKey, config)

				responses := []error{tt.serverError}
				if tt.wantAttempts == 2 {
					responses = append(responses, nil)
				}
				start := time.Now()
				attempts, err := invokeWithRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, responses...)
				require.Len(t, attempts, tt.wantAttempts)
				require.Equal(t, tt.wantStatus, status.Code(err))
				if tt.wantStatus == codes.ResourceExhausted {
					require.ErrorIs(t, err, tt.serverError)
					require.Zero(t, time.Since(start))
				}
				if tt.wantAttempts == 2 && tt.serverError == withHint {
					require.Equal(t, serverDelay, attempts[1].Sub(attempts[0]))
				}
			})
		})
	}
}

func TestRetryInterceptorAttemptIndexAndMaximumAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := NewGrpcRetryConfig(5 * time.Millisecond)
		config.SetBackoffCoefficient(2)
		config.SetMaximumInterval(20 * time.Millisecond)
		config.SetJitter(0)
		config.SetMaximumAttempts(4)
		ctx := context.WithValue(t.Context(), ConfigKey, config)
		err := status.Error(codes.Unavailable, "retry")
		attempts, finalErr := invokeWithRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, err, err, err, nil)
		require.NoError(t, finalErr)
		require.Len(t, attempts, 4)
		require.Equal(t, 10*time.Millisecond, attempts[1].Sub(attempts[0]))
		require.Equal(t, 20*time.Millisecond, attempts[2].Sub(attempts[1]))
		require.Equal(t, 20*time.Millisecond, attempts[3].Sub(attempts[2]))

		config.SetMaximumAttempts(2)
		attempts, finalErr = invokeWithRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, err, err, nil)
		require.ErrorIs(t, finalErr, err)
		require.Len(t, attempts, 2)

		config.SetMaximumAttempts(UnlimitedMaximumAttempts)
		attempts, finalErr = invokeWithRetry(NewRetryOptionsInterceptor(&atomic.Bool{}), ctx, err, err, err, nil)
		require.NoError(t, finalErr)
		require.Len(t, attempts, 4)
	})
}

func TestRetryInterceptorCancellationWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ConfigKey, testGrpcRetryConfig(5)))
		defer cancel()
		var attempts []time.Time
		done := make(chan error, 1)
		go func() {
			done <- NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, "test-method", nil, nil, nil,
				func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
					attempts = append(attempts, time.Now())
					return status.Error(codes.Unavailable, "retry")
				},
			)
		}()
		synctest.Wait()
		require.Len(t, attempts, 1)
		cancel()
		err := <-done
		require.Equal(t, codes.Canceled, status.Code(err))
		require.Len(t, attempts, 1)
	})
}

func TestRetryInterceptorAttemptMetadata(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.WithValue(t.Context(), ConfigKey, testGrpcRetryConfig(2))
		err := status.Error(codes.Unavailable, "retry")
		var attemptHeaders [][]string
		interceptor := NewRetryOptionsInterceptor(&atomic.Bool{})
		finalErr := interceptor(ctx, "test-method", nil, nil, nil,
			func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				md, _ := metadata.FromOutgoingContext(ctx)
				attemptHeaders = append(attemptHeaders, md.Get("x-retry-attempt"))
				if len(attemptHeaders) == 1 {
					return err
				}
				return nil
			},
		)
		require.NoError(t, finalErr)
		require.Len(t, attemptHeaders, 2)
		require.Empty(t, attemptHeaders[0])
		require.Equal(t, []string{"1"}, attemptHeaders[1])
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

// invokeWithRetry runs the retry interceptor directly, returning each attempt's start time.
// Attempt N returns responses[N-1], or nil once responses run out.
func invokeWithRetry(interceptor grpc.UnaryClientInterceptor, ctx context.Context, responses ...error) ([]time.Time, error) {
	var attempts []time.Time
	err := interceptor(ctx, "test-method", nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			attempts = append(attempts, time.Now())
			if len(attempts) <= len(responses) {
				return responses[len(attempts)-1]
			}
			return nil
		},
	)
	return attempts, err
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

func TestIsRetryablePreservesErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		code  codes.Code
		retry bool
	}{
		{codes.DeadlineExceeded, false},
		{codes.Canceled, false},
		{codes.PermissionDenied, false},
		{codes.Unauthenticated, false},
		{codes.InvalidArgument, false},
		{codes.NotFound, false},
		{codes.AlreadyExists, false},
		{codes.Unavailable, true},
		{codes.ResourceExhausted, true},
		{codes.Aborted, true},
		{codes.Unknown, true},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			raw := status.Error(tc.code, "operation failed")
			typed := serviceerror.FromStatus(status.Convert(raw))
			for name, err := range map[string]error{
				"raw":     raw,
				"typed":   typed,
				"wrapped": fmt.Errorf("operation: %w", typed),
			} {
				t.Run(name, func(t *testing.T) {
					require.Equal(t, tc.retry, IsRetryable(err, &atomic.Bool{}))
				})
			}
		})
	}
	for _, err := range []error{nil, context.Canceled, context.DeadlineExceeded} {
		require.False(t, IsRetryable(err, &atomic.Bool{}))
	}
	for _, exclude := range []bool{false, true} {
		var flag atomic.Bool
		flag.Store(exclude)
		for _, err := range []error{status.Error(codes.Internal, "internal"), serviceerror.NewInternal("internal")} {
			require.Equal(t, !exclude, IsRetryable(err, &flag))
		}
	}
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
