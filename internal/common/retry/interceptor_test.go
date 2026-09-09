package retry

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	errordetailspb "go.temporal.io/api/errordetails/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
