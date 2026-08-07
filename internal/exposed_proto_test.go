package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/converter"
)

// failureProvider is the interface callers use with errors.As to extract the
// underlying proto Failure from SDK error types that embed temporalError.
type failureProvider interface {
	Failure() *failurepb.Failure
}

// TestPollWorkflowUpdate_SuccessResult_GetPayloads verifies that a successful
// update result (represented as an EncodedValue wrapping Payloads) supports
// extraction of the raw *commonpb.Payloads via converter.GetPayloads.
func TestPollWorkflowUpdate_SuccessResult_GetPayloads(t *testing.T) {
	require := require.New(t)
	dc := converter.GetDefaultDataConverter()

	// Simulate the payloads that PollWorkflowUpdate would return on success.
	payloads, err := dc.ToPayloads("update-result-value")
	require.NoError(err)

	// This is the same construction used by workflowClientInterceptor.PollWorkflowUpdate
	// on the Outcome_Success branch.
	result := newEncodedValue(payloads, dc)

	// Verify that converter.GetPayloads extracts the raw payloads.
	got := converter.GetPayloads(result)
	require.NotNil(got)
	require.Equal(payloads, got)
	require.Len(got.GetPayloads(), 1)

	// Also verify that the normal Get path still works.
	var decoded string
	require.NoError(result.Get(&decoded))
	require.Equal("update-result-value", decoded)
}

// TestPollWorkflowUpdate_FailureError_ExposesFailure verifies that a failed
// update result (converted from a proto Failure to an error) exposes the
// original Failure via the Failure() method, including stack trace and details.
func TestPollWorkflowUpdate_FailureError_ExposesFailure(t *testing.T) {
	require := require.New(t)
	fc := GetDefaultFailureConverter()
	dc := converter.GetDefaultDataConverter()

	details, err := dc.ToPayloads("failure-detail", 42)
	require.NoError(err)

	// Simulate the Failure proto that PollWorkflowUpdate would receive on failure.
	originalFailure := &failurepb.Failure{
		Message:    "update failed",
		StackTrace: "goroutine 1 [running]:\nmain.updateHandler()\n\t/app/main.go:42",
		FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
			ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
				Type:         "UpdateValidationError",
				NonRetryable: true,
				Details:      details,
			},
		},
	}

	// This is what workflowClientInterceptor.PollWorkflowUpdate does on Outcome_Failure.
	updateErr := fc.FailureToError(originalFailure)

	// Verify the error exposes Failure() via the failureProvider interface.
	var fp failureProvider
	require.True(errors.As(updateErr, &fp), "error should satisfy failureProvider interface")

	failure := fp.Failure()
	require.NotNil(failure)
	require.Equal("update failed", failure.GetMessage())
	require.Contains(failure.GetStackTrace(), "main.updateHandler")
	require.Equal("UpdateValidationError", failure.GetApplicationFailureInfo().GetType())
	require.True(failure.GetApplicationFailureInfo().GetNonRetryable())
	require.Len(failure.GetApplicationFailureInfo().GetDetails().GetPayloads(), 2)
}

// TestActivityResult_SuccessPayloads verifies that an activity success result
// (represented as an EncodedValue) supports converter.GetPayloads.
func TestActivityResult_SuccessPayloads(t *testing.T) {
	require := require.New(t)
	dc := converter.GetDefaultDataConverter()

	// Simulate the payloads from a completed activity.
	payloads, err := dc.ToPayloads("activity-result")
	require.NoError(err)

	result := newEncodedValue(payloads, dc)

	got := converter.GetPayloads(result)
	require.NotNil(got)
	require.Equal(payloads, got)
}

// TestActivityResult_FailureExposesFailure verifies that an activity failure
// error exposes the original Failure proto via the Failure() method, including
// the full cause chain with stack trace and details.
func TestActivityResult_FailureExposesFailure(t *testing.T) {
	require := require.New(t)
	fc := GetDefaultFailureConverter()
	dc := converter.GetDefaultDataConverter()

	details, err := dc.ToPayloads("error-details", 99)
	require.NoError(err)

	// Simulate the failure proto from a failed activity.
	originalFailure := &failurepb.Failure{
		Message: "activity failed",
		FailureInfo: &failurepb.Failure_ActivityFailureInfo{
			ActivityFailureInfo: &failurepb.ActivityFailureInfo{
				ScheduledEventId: 5,
				StartedEventId:   6,
				Identity:         "worker-1",
				ActivityType:     &commonpb.ActivityType{Name: "ProcessOrder"},
				RetryState:       enumspb.RETRY_STATE_MAXIMUM_ATTEMPTS_REACHED,
			},
		},
		Cause: &failurepb.Failure{
			Message:    "order validation failed",
			StackTrace: "goroutine 1 [running]:\nmain.processOrder()\n\t/app/activities.go:55",
			FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
				ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
					Type:         "ValidationError",
					NonRetryable: false,
					Details:      details,
				},
			},
		},
	}

	activityErr := fc.FailureToError(originalFailure)

	// The top-level error should be an ActivityError.
	var actErr *ActivityError
	require.True(errors.As(activityErr, &actErr))

	// ActivityError should expose Failure() via the failureProvider interface.
	var fp failureProvider
	require.True(errors.As(activityErr, &fp), "ActivityError should satisfy failureProvider")

	failure := fp.Failure()
	require.NotNil(failure)
	require.Equal("activity failed", failure.GetMessage())
	require.Equal(int64(5), failure.GetActivityFailureInfo().GetScheduledEventId())

	// Unwrap to get the cause (ApplicationError) and verify its Failure() too.
	causeErr := errors.Unwrap(actErr)
	require.NotNil(causeErr)

	var causeFP failureProvider
	require.True(errors.As(causeErr, &causeFP), "cause ApplicationError should satisfy failureProvider")

	causeFailure := causeFP.Failure()
	require.NotNil(causeFailure)
	require.Equal("order validation failed", causeFailure.GetMessage())
	require.Contains(causeFailure.GetStackTrace(), "main.processOrder")
	require.Equal("ValidationError", causeFailure.GetApplicationFailureInfo().GetType())
	require.Len(causeFailure.GetApplicationFailureInfo().GetDetails().GetPayloads(), 2)
}

// TestGetPayloads_NonImplementor verifies that converter.GetPayloads returns
// nil when called with a type that does not implement ValuesPayloads.
func TestGetPayloads_NonImplementor(t *testing.T) {
	type noPayloads struct{}
	got := converter.GetPayloads(noPayloads{})
	require.Nil(t, got)
}

// TestGetPayloads_NilInput verifies that converter.GetPayloads handles nil
// input gracefully.
func TestGetPayloads_NilInput(t *testing.T) {
	got := converter.GetPayloads(nil)
	require.Nil(t, got)
}

// TestEncodedValues_GetPayloads verifies that the multi-value EncodedValues
// type also supports converter.GetPayloads.
func TestEncodedValues_GetPayloads(t *testing.T) {
	require := require.New(t)
	dc := converter.GetDefaultDataConverter()

	payloads, err := dc.ToPayloads("value1", 42)
	require.NoError(err)

	values := newEncodedValues(payloads, dc)

	got := converter.GetPayloads(values)
	require.NotNil(got)
	require.Equal(payloads, got)
	require.Len(got.GetPayloads(), 2)

	// Verify normal Get still works.
	var s string
	var n int
	require.NoError(values.Get(&s, &n))
	require.Equal("value1", s)
	require.Equal(42, n)
}
