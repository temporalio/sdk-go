package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"
)

func TestActivityPoll_SetsPollerGroupIdAndUpdatesTracker(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: "test-tq",
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	poller := newActivityTaskPoller(&noopActivityTaskHandler{}, service, params)

	// First poll: no groups known yet, so PollerGroupId should be empty.
	// Server returns groups in the response.
	service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.PollActivityTaskQueueRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollActivityTaskQueueResponse, error) {
			require.Empty(t, req.PollerGroupId, "first poll should have empty poller group id")
			return &workflowservice.PollActivityTaskQueueResponse{
				TaskToken: []byte("token"),
				PollerGroupInfos: []*taskqueuepb.PollerGroupInfo{
					{Id: "group-1", Weight: 1.0},
					{Id: "group-2", Weight: 1.0},
				},
			}, nil
		})

	ctx := context.Background()
	_, err := poller.poll(ctx)
	require.NoError(t, err)

	// Second poll: tracker now has groups, so PollerGroupId should be set.
	service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.PollActivityTaskQueueRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollActivityTaskQueueResponse, error) {
			require.NotEmpty(t, req.PollerGroupId, "second poll should have a poller group id")
			require.Contains(t, []string{"group-1", "group-2"}, req.PollerGroupId)
			return &workflowservice.PollActivityTaskQueueResponse{}, nil
		})

	_, err = poller.poll(ctx)
	require.NoError(t, err)
}

type noopActivityTaskHandler struct{}

func (h *noopActivityTaskHandler) Execute(string, *workflowservice.PollActivityTaskQueueResponse) (interface{}, error) {
	return nil, nil
}

func TestWorkflowPoll_SetsPollerGroupIdAndUpdatesTracker(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: "test-tq",
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	processor := newWorkflowTaskProcessor(
		newWorkflowTaskHandler(params, nil, newRegistry()),
		nil,
		service,
		params,
		"sticky-uuid",
	)
	poller := processor.createPoller(NonSticky).(*workflowTaskPoller)

	// First poll: no groups known yet.
	service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.PollWorkflowTaskQueueRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollWorkflowTaskQueueResponse, error) {
			require.Empty(t, req.PollerGroupId, "first poll should have empty poller group id")
			return &workflowservice.PollWorkflowTaskQueueResponse{
				PollerGroupInfos: []*taskqueuepb.PollerGroupInfo{
					{Id: "wf-group-a", Weight: 1.0},
				},
			}, nil
		})

	ctx := context.Background()
	_, err := poller.poll(ctx)
	require.NoError(t, err)

	// Second poll: tracker has groups.
	service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.PollWorkflowTaskQueueRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollWorkflowTaskQueueResponse, error) {
			require.Equal(t, "wf-group-a", req.PollerGroupId)
			return &workflowservice.PollWorkflowTaskQueueResponse{}, nil
		})

	_, err = poller.poll(ctx)
	require.NoError(t, err)
}

func TestNexusPoll_SetsPollerGroupIdAndUpdatesTracker(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: "test-tq",
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	poller := newNexusTaskPoller(nil, service, params)

	// First poll: no groups known yet.
	service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.PollNexusTaskQueueRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollNexusTaskQueueResponse, error) {
			require.Empty(t, req.PollerGroupId, "first poll should have empty poller group id")
			return &workflowservice.PollNexusTaskQueueResponse{
				PollerGroupInfos: []*taskqueuepb.PollerGroupInfo{
					{Id: "nexus-group-x", Weight: 1.0},
				},
			}, nil
		})

	ctx := context.Background()
	_, err := poller.poll(ctx)
	require.NoError(t, err)

	// Second poll: tracker has groups.
	service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.PollNexusTaskQueueRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.PollNexusTaskQueueResponse, error) {
			require.Equal(t, "nexus-group-x", req.PollerGroupId)
			return &workflowservice.PollNexusTaskQueueResponse{}, nil
		})

	_, err = poller.poll(ctx)
	require.NoError(t, err)
}

func TestQueryResponse_ForwardsPollerGroupId(t *testing.T) {
	taskQueue := "tq1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowTaskStarted(3),
	}

	task := createQueryTask(testEvents, 3, "HelloWorld_Workflow", queryType)
	task.PollerGroupId = "test-poller-group-42"

	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: taskQueue,
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	reg := newRegistry()
	reg.RegisterWorkflowWithOptions(helloWorldWorkflowFunc, RegisterWorkflowOptions{Name: "HelloWorld_Workflow"})

	taskHandler := newWorkflowTaskHandler(params, nil, reg)
	wftask := workflowTask{task: task}

	wfctx, err := taskHandler.GetOrCreateWorkflowContext(task, wftask.historyIterator)
	require.NoError(t, err)

	response, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	require.NoError(t, err)
	require.NotNil(t, response)

	queryResp, ok := response.rawRequest.(*workflowservice.RespondQueryTaskCompletedRequest)
	require.True(t, ok)
	require.Equal(t, "test-poller-group-42", queryResp.PollerGroupId)
}

func TestQueryResponse_ForwardsPollerGroupIdOnError(t *testing.T) {
	taskQueue := "tq1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventWorkflowTaskStarted(3),
	}

	task := createQueryTask(testEvents, 3, "HelloWorld_Workflow", "nonexistent-query-type")
	task.PollerGroupId = "test-poller-group-err"

	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: taskQueue,
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	reg := newRegistry()
	reg.RegisterWorkflowWithOptions(helloWorldWorkflowFunc, RegisterWorkflowOptions{Name: "HelloWorld_Workflow"})

	taskHandler := newWorkflowTaskHandler(params, nil, reg)
	wftask := workflowTask{task: task}

	wfctx, err := taskHandler.GetOrCreateWorkflowContext(task, wftask.historyIterator)
	require.NoError(t, err)

	response, err := taskHandler.ProcessWorkflowTask(&wftask, wfctx, nil)
	wfctx.Unlock(err)
	require.NoError(t, err)
	require.NotNil(t, response)

	queryResp, ok := response.rawRequest.(*workflowservice.RespondQueryTaskCompletedRequest)
	require.True(t, ok)
	require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, queryResp.CompletedType)
	require.Equal(t, "test-poller-group-err", queryResp.PollerGroupId)
}

// noopNexusHandler is a minimal nexus.Handler that succeeds on CancelOperation.
type noopNexusHandler struct {
	nexus.UnimplementedHandler
}

func (h *noopNexusHandler) CancelOperation(_ context.Context, service, operation, token string, _ nexus.CancelOperationOptions) error {
	return nil
}

func TestNexusCompletion_ForwardsPollerGroupId(t *testing.T) {
	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: "test-tq",
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	handler := newNexusTaskHandler(
		&noopNexusHandler{},
		params.Identity,
		params.Namespace,
		params.TaskQueue,
		nil,
		params.DataConverter,
		params.FailureConverter,
		params.Logger,
		params.MetricsHandler,
		nil,
	)

	// Call Execute with a CancelOperation request. The noopNexusHandler succeeds,
	// so we get a completion. Verify PollerGroupId is forwarded from the response.
	task := &workflowservice.PollNexusTaskQueueResponse{
		TaskToken:     []byte("token"),
		PollerGroupId: "nexus-pg-complete",
		Request: &nexuspb.Request{
			Variant: &nexuspb.Request_CancelOperation{
				CancelOperation: &nexuspb.CancelOperationRequest{
					Service:   "test-service",
					Operation: "test-op",
				},
			},
		},
	}

	completedReq, failedReq, err := handler.Execute(task)
	require.NoError(t, err)
	require.Nil(t, failedReq)
	require.NotNil(t, completedReq)
	require.Equal(t, "nexus-pg-complete", completedReq.PollerGroupId)
}

func TestNexusFailure_ForwardsPollerGroupId(t *testing.T) {
	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: "test-tq",
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	handler := newNexusTaskHandler(
		nil, // no nexus handler needed — nil request variant triggers failure
		params.Identity,
		params.Namespace,
		params.TaskQueue,
		nil,
		params.DataConverter,
		params.FailureConverter,
		params.Logger,
		params.MetricsHandler,
		nil,
	)

	// Call Execute with no valid request variant. newNexusOperationContext returns
	// a handler error, which goes through fillInFailure. Verify PollerGroupId is
	// forwarded from the poll response.
	task := &workflowservice.PollNexusTaskQueueResponse{
		TaskToken:     []byte("token"),
		PollerGroupId: "nexus-pg-fail",
	}

	completedReq, failedReq, err := handler.Execute(task)
	require.NoError(t, err)
	require.Nil(t, completedReq)
	require.NotNil(t, failedReq)
	require.Equal(t, "nexus-pg-fail", failedReq.PollerGroupId)
}

func TestGrpcTooLargeQueryResponse_ForwardsPollerGroupId(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	params := workerExecutionParameters{
		Namespace: "test-ns",
		TaskQueue: "test-tq",
		cache:     NewWorkerCache(),
	}
	ensureRequiredParams(&params)

	processor := newWorkflowTaskProcessor(
		newWorkflowTaskHandler(params, nil, newRegistry()),
		nil,
		service,
		params,
		"sticky-uuid",
	)

	task := &workflowservice.PollWorkflowTaskQueueResponse{
		TaskToken:     []byte("token"),
		PollerGroupId: "grpc-too-large-pg",
		WorkflowType:  &commonpb.WorkflowType{Name: "test-wf"},
	}

	// Capture the RespondQueryTaskCompleted request.
	service.EXPECT().RespondQueryTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.RespondQueryTaskCompletedRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.RespondQueryTaskCompletedResponse, error) {
			require.Equal(t, "grpc-too-large-pg", req.PollerGroupId)
			require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, req.CompletedType)
			return &workflowservice.RespondQueryTaskCompletedResponse{}, nil
		})

	// Simulate the GRPC too large fallback path for a query task.
	queryCompletion := &workflowTaskCompletion{
		rawRequest: &workflowservice.RespondQueryTaskCompletedRequest{},
	}
	_, _ = processor.reportGrpcMessageTooLarge(
		context.Background(),
		queryCompletion,
		task,
		fmt.Errorf("grpc message too large"),
	)
}
