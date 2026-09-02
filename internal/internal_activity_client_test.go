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
