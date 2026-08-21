package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	activitypb "go.temporal.io/api/activity/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
)

// headerCheckInterceptor is a ClientInterceptor that verifies the header is
// present on the context when ExecuteActivity is called. This ensures that
// contextWithNewHeader is called before the interceptor chain runs, so
// interceptors (like the tracing interceptor) can read/write headers.
type headerCheckInterceptor struct {
	ClientInterceptorBase
	headerWasPresent bool
}

func (h *headerCheckInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &headerCheckOutbound{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		parent:                        h,
	}
}

type headerCheckOutbound struct {
	ClientOutboundInterceptorBase
	parent *headerCheckInterceptor
}

func (h *headerCheckOutbound) ExecuteActivity(
	ctx context.Context,
	in *ClientExecuteActivityInput,
) (ClientActivityHandle, error) {
	h.parent.headerWasPresent = Header(ctx) != nil
	// Return an error to short-circuit the rest of the chain (avoids needing a
	// real gRPC connection for the base interceptor).
	return nil, fmt.Errorf("short-circuit")
}

func TestExecuteActivityHeaderAvailableToInterceptors(t *testing.T) {
	interceptor := &headerCheckInterceptor{}

	client := NewServiceClient(nil, nil, ClientOptions{
		Interceptors: []ClientInterceptor{interceptor},
	})
	// Pre-set capabilities so ensureInitialized doesn't make a gRPC call.
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	// Register a dummy activity so getValidatedActivityFunction succeeds.
	dummyActivity := func(ctx context.Context) error { return nil }
	client.registry.RegisterActivityWithOptions(dummyActivity, RegisterActivityOptions{})

	_, err := client.ExecuteActivity(t.Context(), ClientStartActivityOptions{
		TaskQueue:           "test-tq",
		ID:                  "test-activity-id",
		StartToCloseTimeout: 1,
	}, dummyActivity)
	// We expect the short-circuit error from our interceptor.
	require.ErrorContains(t, err, "short-circuit")
	require.True(t, interceptor.headerWasPresent,
		"Header should be set on context before interceptor chain runs")
}

func TestExecuteActivityFromLinklessNexusRequestOmitsOnConflictOptions(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	var request *workflowservice.StartActivityExecutionRequest
	service.EXPECT().
		StartActivityExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.StartActivityExecutionRequest, _ ...any) (*workflowservice.StartActivityExecutionResponse, error) {
			request = req
			return &workflowservice.StartActivityExecutionResponse{RunId: "run-id"}, nil
		})

	ctx := context.WithValue(t.Context(), nexusOperationContextKey, &NexusOperationContext{})
	_, err := client.ExecuteActivity(ctx, ClientStartActivityOptions{
		ID:                     "activity-id",
		TaskQueue:              "task-queue",
		ScheduleToCloseTimeout: time.Minute,
	}, "activity")
	require.NoError(t, err)
	require.Empty(t, request.GetLinks())
	require.Empty(t, request.GetCompletionCallbacks())
	require.Nil(t, request.GetOnConflictOptions())
}

// TestDescribeActivityPayloadOptInsReachRequest asserts that the four api#792 opt-in flags
// travel from the caller's options all the way to the DescribeActivityExecution request, and
// that the zero-value options ask for nothing.
func TestDescribeActivityPayloadOptInsReachRequest(t *testing.T) {
	describeWith := func(t *testing.T, options ClientDescribeActivityOptions) *workflowservice.DescribeActivityExecutionRequest {
		t.Helper()
		service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
		client := NewServiceClient(service, nil, ClientOptions{})
		client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

		var request *workflowservice.DescribeActivityExecutionRequest
		service.EXPECT().
			DescribeActivityExecution(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.DescribeActivityExecutionRequest, _ ...any) (*workflowservice.DescribeActivityExecutionResponse, error) {
				request = req
				return &workflowservice.DescribeActivityExecutionResponse{
					Info: &activitypb.ActivityExecutionInfo{
						ActivityId: "activity-id",
						// A real server always sends this; the describe conversion
						// reads it without a nil guard.
						SearchAttributes: &commonpb.SearchAttributes{},
					},
				}, nil
			})

		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})
		_, err := handle.Describe(t.Context(), options)
		require.NoError(t, err)
		return request
	}

	t.Run("default asks for nothing", func(t *testing.T) {
		request := describeWith(t, ClientDescribeActivityOptions{})
		require.False(t, request.GetIncludeInput())
		require.False(t, request.GetIncludeOutcome())
		require.False(t, request.GetIncludeHeartbeatDetails())
		require.False(t, request.GetIncludeLastFailure())
	})

	t.Run("each flag is forwarded", func(t *testing.T) {
		request := describeWith(t, ClientDescribeActivityOptions{
			IncludeInput:            true,
			IncludeOutcome:          true,
			IncludeHeartbeatDetails: true,
			IncludeLastFailure:      true,
		})
		require.True(t, request.GetIncludeInput())
		require.True(t, request.GetIncludeOutcome())
		require.True(t, request.GetIncludeHeartbeatDetails())
		require.True(t, request.GetIncludeLastFailure())
	})
}
