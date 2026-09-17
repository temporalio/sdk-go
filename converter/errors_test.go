package converter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowTaskFailureError_UnwrapAndMatch(t *testing.T) {
	cause := errors.New("kms throttled: 503")
	err := NewWorkflowTaskFailureError(cause)

	// The concrete cause is reachable via errors.Is.
	require.True(t, errors.Is(err, cause))

	// The marker is reachable via errors.As even when further wrapped, mirroring
	// how the SDK re-wraps codec errors on the decode path with %w.
	wrapped := fmt.Errorf("unable to decode the workflow function input payload with error: %w", err)
	var marker *WorkflowTaskFailureError
	require.True(t, errors.As(wrapped, &marker))
	require.Equal(t, cause, marker.cause)

	// Error() delegates to the wrapped cause.
	require.Equal(t, cause.Error(), err.Error())
}

func TestWorkflowTaskFailureError_NilCause(t *testing.T) {
	err := &WorkflowTaskFailureError{}
	require.Equal(t, "workflow task failure requested by payload codec", err.Error())
	require.Nil(t, errors.Unwrap(err))
}
