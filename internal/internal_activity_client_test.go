package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
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

func TestUpdateActivityOptionsMask(t *testing.T) {
	newClient := func(t *testing.T, request **workflowservice.UpdateActivityExecutionOptionsRequest) *WorkflowClient {
		t.Helper()
		service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
		service.EXPECT().
			UpdateActivityExecutionOptions(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.UpdateActivityExecutionOptionsRequest, _ ...any) (*workflowservice.UpdateActivityExecutionOptionsResponse, error) {
				*request = req
				// These tests assert on the request only. What comes back is whatever this
				// mock is told to say, so asserting on it would test the mock; the real
				// server's resolved options are covered by the functional tests.
				return &workflowservice.UpdateActivityExecutionOptionsResponse{}, nil
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

		_, err := handle.UpdateOptions(t.Context(),
			ClientActivityOptionsKeys.TaskQueue.ValueSet("new-tq"),
			ClientActivityOptionsKeys.StartToCloseTimeout.ValueSet(90*time.Second))
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"task_queue.name", "start_to_close_timeout"},
			request.GetUpdateMask().GetPaths())
		require.False(t, request.GetRestoreOriginal())
		require.Equal(t, "new-tq", request.GetActivityOptions().GetTaskQueue().GetName())
		require.Equal(t, 90*time.Second, request.GetActivityOptions().GetStartToCloseTimeout().AsDuration())
	})

	t.Run("ValueSet of zero sends an explicit zero", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context(),
			ClientActivityOptionsKeys.HeartbeatTimeout.ValueSet(0))
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"heartbeat_timeout"}, request.GetUpdateMask().GetPaths())
		// Present and zero, which is distinct from absent: the caller asked for zero.
		require.NotNil(t, request.GetActivityOptions().GetHeartbeatTimeout())
		require.Zero(t, request.GetActivityOptions().GetHeartbeatTimeout().AsDuration())
	})

	t.Run("ValueUnset names the path but leaves the field absent", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context(),
			ClientActivityOptionsKeys.HeartbeatTimeout.ValueUnset())
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"heartbeat_timeout"}, request.GetUpdateMask().GetPaths())
		// Absent, which is how the server is told to clear the option.
		require.Nil(t, request.GetActivityOptions().GetHeartbeatTimeout())
	})

	t.Run("a repeated key resolves to its last update", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context(),
			ClientActivityOptionsKeys.HeartbeatTimeout.ValueSet(5*time.Second),
			ClientActivityOptionsKeys.HeartbeatTimeout.ValueUnset())
		require.NoError(t, err)
		// The later unset wins, and the path is named once.
		require.ElementsMatch(t, []string{"heartbeat_timeout"}, request.GetUpdateMask().GetPaths())
		require.Nil(t, request.GetActivityOptions().GetHeartbeatTimeout())
	})

	t.Run("a hand-built zero update is rejected, not silently ignored", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context(), ClientActivityOptionsUpdate{})
		require.ErrorContains(t, err, "not a valid option update")
		require.Nil(t, request)
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

	t.Run("an update naming nothing is rejected before the interceptor chain", func(t *testing.T) {
		var request *workflowservice.UpdateActivityExecutionOptionsRequest
		client := newClient(t, &request)
		recorder := &recordingOutboundInterceptor{}
		client.interceptor = recorder.intercept(client.interceptor)
		handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

		_, err := handle.UpdateOptions(t.Context())
		require.ErrorContains(t, err, "at least one option update")
		require.Nil(t, request)
		require.Zero(t, recorder.updateCalls)
	})
}

// Asserts the request fields the server never sends back.
func TestActivityOperatorCommandRequestFields(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{Identity: "test-identity"})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	var pause *workflowservice.PauseActivityExecutionRequest
	var unpause *workflowservice.UnpauseActivityExecutionRequest

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
	require.Equal(t, "pause-reason", pause.GetReason())
	require.Equal(t, "unpause-reason", unpause.GetReason())
	require.Equal(t, 5*time.Second, unpause.GetJitter().AsDuration())

	// A zero jitter is left off the wire rather than sent as an explicit zero duration, so the
	// server applies its own default instead of "no jitter".
	require.NoError(t, handle.Unpause(ctx, ClientUnpauseActivityOptions{}))
	require.Nil(t, unpause.GetJitter())
}

// TestInterceptorReceivesCommandArguments asserts the values a caller passes reach the
// interceptor chain, not merely that the hook fired. A dropped argument between the handle and
// the chain is invisible to a test that only counts calls.
func TestInterceptorReceivesCommandArguments(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().PauseActivityExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.PauseActivityExecutionResponse{}, nil).AnyTimes()
	service.EXPECT().UnpauseActivityExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.UnpauseActivityExecutionResponse{}, nil).AnyTimes()

	client := NewServiceClient(service, nil, ClientOptions{})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
	recorder := &recordingOutboundInterceptor{}
	client.interceptor = recorder.intercept(client.interceptor)
	handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id"})

	ctx := t.Context()
	require.NoError(t, handle.Pause(ctx, ClientPauseActivityOptions{Reason: "pause-reason"}))
	require.NoError(t, handle.Unpause(ctx, ClientUnpauseActivityOptions{
		Reason: "unpause-reason",
		Jitter: 5 * time.Second,
	}))

	require.Equal(t, "pause-reason", recorder.pauseIn.Reason)
	require.Equal(t, "unpause-reason", recorder.unpauseIn.Reason)
	require.Equal(t, 5*time.Second, recorder.unpauseIn.Jitter)
}

// TestUpdateActivityOptionsRestoreIsExclusive asserts the guard the handle cannot reach: the
// server rejects restore-original combined with individual changes, and only a hand-built
// interceptor input can produce that combination.
func TestUpdateActivityOptionsRestoreIsExclusive(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	// No RPC expectation is registered, so reaching the service would fail the mock.
	_, err := client.interceptor.UpdateActivityOptions(t.Context(), &ClientUpdateActivityOptionsInput{
		ActivityID:      "activity-id",
		RestoreOriginal: true,
		Updates: []ClientActivityOptionsUpdate{
			ClientActivityOptionsKeys.HeartbeatTimeout.ValueSet(25 * time.Second),
		},
	})
	require.ErrorContains(t, err, "cannot be combined")
}

// counts UpdateActivityOptions calls
type recordingOutboundInterceptor struct {
	updateCalls int
	pauseIn     *ClientPauseActivityInput
	unpauseIn   *ClientUnpauseActivityInput
	updateIn    *ClientUpdateActivityOptionsInput
}

func (r *recordingOutboundInterceptor) intercept(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &recordingOutbound{ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next}, parent: r}
}

type recordingOutbound struct {
	ClientOutboundInterceptorBase
	parent *recordingOutboundInterceptor
}

func (r *recordingOutbound) UpdateActivityOptions(
	ctx context.Context,
	in *ClientUpdateActivityOptionsInput,
) (*ClientUpdateActivityOptionsOutput, error) {
	r.parent.updateCalls++
	r.parent.updateIn = in
	return r.ClientOutboundInterceptorBase.UpdateActivityOptions(ctx, in)
}

func (r *recordingOutbound) PauseActivity(ctx context.Context, in *ClientPauseActivityInput) error {
	r.parent.pauseIn = in
	return r.ClientOutboundInterceptorBase.PauseActivity(ctx, in)
}

func (r *recordingOutbound) UnpauseActivity(ctx context.Context, in *ClientUnpauseActivityInput) error {
	r.parent.unpauseIn = in
	return r.ClientOutboundInterceptorBase.UnpauseActivity(ctx, in)
}

func TestExecuteActivityFromNexusRequestUsesStableRequestID(t *testing.T) {
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

	ctx := context.WithValue(t.Context(), nexusOperationContextKey, &NexusOperationContext{RequestID: "nexus-request-id"})
	_, err := client.ExecuteActivity(ctx, ClientStartActivityOptions{
		ID:                     "activity-id",
		TaskQueue:              "task-queue",
		ScheduleToCloseTimeout: time.Minute,
	}, "activity")
	require.NoError(t, err)
	require.Equal(t, "nexus-request-id", request.GetRequestId())
	require.Nil(t, request.GetOnConflictOptions())
}

func TestExecuteActivityValidationFailsBeforeStartRPC(t *testing.T) {
	dummyActivity := func(context.Context, any) error { return nil }

	for _, tc := range []struct {
		name         string
		options      ClientStartActivityOptions
		args         []any
		errorContain string
	}{
		{
			name: "missing activity ID",
			options: ClientStartActivityOptions{
				TaskQueue:           "test-task-queue",
				StartToCloseTimeout: time.Minute,
			},
			args:         []any{"value"},
			errorContain: "activity ID is required",
		},
		{
			name: "missing task queue",
			options: ClientStartActivityOptions{
				ID:                  "test-activity-id",
				StartToCloseTimeout: time.Minute,
			},
			args:         []any{"value"},
			errorContain: "task queue is required",
		},
		{
			name: "negative schedule to close timeout",
			options: ClientStartActivityOptions{
				ID:                     "test-activity-id",
				TaskQueue:              "test-task-queue",
				ScheduleToCloseTimeout: -time.Second,
				StartToCloseTimeout:    time.Minute,
			},
			args:         []any{"value"},
			errorContain: "negative ScheduleToCloseTimeout",
		},
		{
			name: "missing both close timeouts",
			options: ClientStartActivityOptions{
				ID:        "test-activity-id",
				TaskQueue: "test-task-queue",
			},
			args:         []any{"value"},
			errorContain: "at least one of ScheduleToCloseTimeout and StartToCloseTimeout is required",
		},
		{
			name: "invalid activity args",
			options: ClientStartActivityOptions{
				ID:                  "test-activity-id",
				TaskQueue:           "test-task-queue",
				StartToCloseTimeout: time.Minute,
			},
			args:         []any{make(chan int)},
			errorContain: "unsupported type: chan int",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			client := NewServiceClient(service, nil, ClientOptions{})
			client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
			client.registry.RegisterActivityWithOptions(dummyActivity, RegisterActivityOptions{})

			service.EXPECT().
				StartActivityExecution(gomock.Any(), gomock.Any()).
				Return(&workflowservice.StartActivityExecutionResponse{RunId: "run-id"}, nil).
				Times(1)

			_, err := client.ExecuteActivity(t.Context(), tc.options, dummyActivity, tc.args...)
			require.ErrorContains(t, err, tc.errorContain)

			_, err = client.ExecuteActivity(t.Context(), ClientStartActivityOptions{
				ID:                  "valid-activity-id",
				TaskQueue:           "test-task-queue",
				StartToCloseTimeout: time.Minute,
			}, dummyActivity, "value")
			require.NoError(t, err)
		})
	}
}
