package retry

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	errordetailspb "go.temporal.io/api/errordetails/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
