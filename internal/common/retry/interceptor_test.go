package retry

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	errordetailspb "go.temporal.io/api/errordetails/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsWorkflowTaskCompletionBufferLost(t *testing.T) {
	st, err := status.New(codes.Aborted, "buffer lost").WithDetails(&errordetailspb.WorkflowTaskCompletionBufferLostFailure{})
	require.NoError(t, err)
	bufferLostErr := st.Err()

	require.True(t, IsWorkflowTaskCompletionBufferLost(bufferLostErr))
	// A plain Aborted without the detail is not buffer loss.
	require.False(t, IsWorkflowTaskCompletionBufferLost(status.Error(codes.Aborted, "other")))
	require.False(t, IsWorkflowTaskCompletionBufferLost(nil))

	// Buffer loss must not be retried at the gRPC layer, though plain Aborted still is.
	require.False(t, IsRetryable(bufferLostErr, &atomic.Bool{}))
	require.True(t, IsRetryable(status.Error(codes.Aborted, "other"), &atomic.Bool{}))
}
