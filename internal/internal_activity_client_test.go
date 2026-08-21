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
	failurepb "go.temporal.io/api/failure/v1"
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

// TestDescribeActivityStripsUnrequestedPayloads asserts that payloads returned by a server that
// ignores the opt-in flags are dropped client-side, so the Has* accessors always agree with what
// the caller asked for.
func TestDescribeActivityStripsUnrequestedPayloads(t *testing.T) {
	newOverSharingService := func(t *testing.T) *workflowservicemock.MockWorkflowServiceClient {
		t.Helper()
		service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
		service.EXPECT().
			DescribeActivityExecution(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *workflowservice.DescribeActivityExecutionRequest, _ ...any) (*workflowservice.DescribeActivityExecutionResponse, error) {
				payloads := &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("x")}}}
				return &workflowservice.DescribeActivityExecutionResponse{
					Info: &activitypb.ActivityExecutionInfo{
						ActivityId:       "activity-id",
						SearchAttributes: &commonpb.SearchAttributes{},
						HeartbeatDetails: payloads,
						LastFailure:      &failurepb.Failure{Message: "boom"},
					},
					Input: payloads,
					Outcome: &activitypb.ActivityExecutionOutcome{
						Value: &activitypb.ActivityExecutionOutcome_Result{Result: payloads},
					},
				}, nil
			})
		return service
	}

	describeWith := func(t *testing.T, options ClientDescribeActivityOptions) *ClientActivityExecutionDescription {
		t.Helper()
		client := NewServiceClient(newOverSharingService(t), nil, ClientOptions{})
		client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})
		desc, err := handle.Describe(t.Context(), options)
		require.NoError(t, err)
		return desc
	}

	t.Run("nothing requested", func(t *testing.T) {
		desc := describeWith(t, ClientDescribeActivityOptions{})
		require.False(t, desc.HasInput())
		require.False(t, desc.HasResult())
		require.False(t, desc.HasHeartbeatDetails())
		require.False(t, desc.HasLastFailure())
		require.ErrorIs(t, desc.GetInput(nil), ErrNoData)
		require.ErrorIs(t, desc.GetResult(nil), ErrNoData)
		require.ErrorIs(t, desc.GetHeartbeatDetails(nil), ErrNoData)
		require.NoError(t, desc.GetFailure())
		require.NoError(t, desc.GetLastFailure())
	})

	t.Run("everything requested", func(t *testing.T) {
		desc := describeWith(t, ClientDescribeActivityOptions{
			IncludeInput:            true,
			IncludeOutcome:          true,
			IncludeHeartbeatDetails: true,
			IncludeLastFailure:      true,
		})
		require.True(t, desc.HasInput())
		require.True(t, desc.HasResult())
		require.True(t, desc.HasHeartbeatDetails())
		require.True(t, desc.HasLastFailure())
	})

	t.Run("each flag is independent", func(t *testing.T) {
		desc := describeWith(t, ClientDescribeActivityOptions{IncludeInput: true})
		require.True(t, desc.HasInput())
		require.False(t, desc.HasResult())
		require.False(t, desc.HasHeartbeatDetails())
		require.False(t, desc.HasLastFailure())
	})
}

// TestUpdateActivityOptionsMask asserts that the field mask names exactly the options the caller
// asked to change, and that the two combinations the server would reject are caught locally.
func TestUpdateActivityOptionsMask(t *testing.T) {
	newClient := func(t *testing.T, request **workflowservice.UpdateActivityExecutionOptionsRequest) *WorkflowClient {
		t.Helper()
		service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
		service.EXPECT().
			UpdateActivityExecutionOptions(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.UpdateActivityExecutionOptionsRequest, _ ...any) (*workflowservice.UpdateActivityExecutionOptionsResponse, error) {
				*request = req
				return &workflowservice.UpdateActivityExecutionOptionsResponse{
					ActivityOptions: req.GetActivityOptions(),
				}, nil
			}).
			AnyTimes()
		client := NewServiceClient(service, nil, ClientOptions{})
		client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
		return client
	}

	t.Run("mask names only the changed options", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		options, err := handle.UpdateOptions(t.Context(), ClientActivityOptionsChanges{
			TaskQueue:           &TaskQueueChange{Value: "new-tq"},
			StartToCloseTimeout: &DurationChange{Value: 90 * time.Second},
		})
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"task_queue.name", "start_to_close_timeout"},
			request.GetUpdateMask().GetPaths())
		require.False(t, request.GetRestoreOriginal())
		require.Equal(t, "new-tq", options.TaskQueue)
		require.Equal(t, 90*time.Second, options.StartToCloseTimeout)
	})

	t.Run("a zero-valued change still names its path", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context(), ClientActivityOptionsChanges{
			HeartbeatTimeout: &DurationChange{},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"heartbeat_timeout"}, request.GetUpdateMask().GetPaths())
	})

	t.Run("restore sends an empty mask", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.RestoreOriginalOptions(t.Context())
		require.NoError(t, err)
		require.True(t, request.GetRestoreOriginal())
		require.Empty(t, request.GetUpdateMask().GetPaths())
	})

	t.Run("an update naming nothing is rejected", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context(), ClientActivityOptionsChanges{})
		require.ErrorContains(t, err, "at least one option change")
		require.Nil(t, request)
	})
}

// TestActivityOperatorCommandRequestFields asserts the request fields the server never echoes
// back, so they cannot be checked by observing activity state: identity, a fresh request ID, and
// the reason and jitter the caller supplied.
func TestActivityOperatorCommandRequestFields(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{Identity: "test-identity"})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	var pause *workflowservice.PauseActivityExecutionRequest
	var unpause *workflowservice.UnpauseActivityExecutionRequest
	var reset *workflowservice.ResetActivityExecutionRequest
	var update *workflowservice.UpdateActivityExecutionOptionsRequest

	service.EXPECT().PauseActivityExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.PauseActivityExecutionRequest, _ ...any) (*workflowservice.PauseActivityExecutionResponse, error) {
			pause = req
			return &workflowservice.PauseActivityExecutionResponse{}, nil
		}).AnyTimes()
	service.EXPECT().UnpauseActivityExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.UnpauseActivityExecutionRequest, _ ...any) (*workflowservice.UnpauseActivityExecutionResponse, error) {
			unpause = req
			return &workflowservice.UnpauseActivityExecutionResponse{}, nil
		}).AnyTimes()
	service.EXPECT().ResetActivityExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.ResetActivityExecutionRequest, _ ...any) (*workflowservice.ResetActivityExecutionResponse, error) {
			reset = req
			return &workflowservice.ResetActivityExecutionResponse{}, nil
		}).AnyTimes()
	service.EXPECT().UpdateActivityExecutionOptions(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.UpdateActivityExecutionOptionsRequest, _ ...any) (*workflowservice.UpdateActivityExecutionOptionsResponse, error) {
			update = req
			return &workflowservice.UpdateActivityExecutionOptionsResponse{}, nil
		}).AnyTimes()

	handle := client.GetActivityHandle(ClientGetActivityHandleOptions{
		ActivityID: "activity-id",
		RunID:      "run-id",
	})
	ctx := t.Context()
	require.NoError(t, handle.Pause(ctx, ClientPauseActivityOptions{Reason: "pause-reason"}))
	require.NoError(t, handle.Unpause(ctx, ClientUnpauseActivityOptions{
		Reason: "unpause-reason",
		Jitter: 5 * time.Second,
	}))
	require.NoError(t, handle.Reset(ctx, ClientResetActivityOptions{Jitter: 7 * time.Second}))
	_, err := handle.UpdateOptions(ctx, ClientActivityOptionsChanges{
		HeartbeatTimeout: &DurationChange{Value: 25 * time.Second},
	})
	require.NoError(t, err)

	for name, got := range map[string]struct {
		activityID string
		runID      string
		identity   string
		requestID  string
	}{
		"pause":   {pause.GetActivityId(), pause.GetRunId(), pause.GetIdentity(), pause.GetRequestId()},
		"unpause": {unpause.GetActivityId(), unpause.GetRunId(), unpause.GetIdentity(), unpause.GetRequestId()},
		"reset":   {reset.GetActivityId(), reset.GetRunId(), reset.GetIdentity(), reset.GetRequestId()},
		"update":  {update.GetActivityId(), update.GetRunId(), update.GetIdentity(), update.GetRequestId()},
	} {
		require.Equal(t, "activity-id", got.activityID, name)
		require.Equal(t, "run-id", got.runID, name)
		require.Equal(t, "test-identity", got.identity, name)
		require.NotEmpty(t, got.requestID, name)
	}

	// Request IDs must be fresh per call, not reused across commands.
	require.ElementsMatch(t,
		[]string{pause.GetRequestId(), unpause.GetRequestId(), reset.GetRequestId(), update.GetRequestId()},
		uniqueStrings(pause.GetRequestId(), unpause.GetRequestId(), reset.GetRequestId(), update.GetRequestId()))

	require.Equal(t, "pause-reason", pause.GetReason())
	require.Equal(t, "unpause-reason", unpause.GetReason())
	require.Equal(t, 5*time.Second, unpause.GetJitter().AsDuration())
	require.Equal(t, 7*time.Second, reset.GetJitter().AsDuration())

	// A zero jitter is left off the wire rather than sent as an explicit zero duration, so the
	// server applies its own default instead of "no jitter".
	require.NoError(t, handle.Unpause(ctx, ClientUnpauseActivityOptions{}))
	require.Nil(t, unpause.GetJitter())
	require.NoError(t, handle.Reset(ctx, ClientResetActivityOptions{}))
	require.Nil(t, reset.GetJitter())
}

func uniqueStrings(values ...string) []string {
	seen := map[string]struct{}{}
	var unique []string
	for _, v := range values {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			unique = append(unique, v)
		}
	}
	return unique
}
