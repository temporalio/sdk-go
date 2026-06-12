package internal

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	workerservicepb "go.temporal.io/api/nexusservices/workerservice/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// TestStopCancelsInFlightHeartbeatRPC verifies that calling stop() on a
// sharedNamespaceWorker cancels an in-flight heartbeat RPC. Without the fix
// (using context.Background() for the RPC), stop() would hang forever because
// the blocked RPC prevents run() from seeing stopC. With the fix
// (heartbeatCtx), stop() cancels the context first, unblocking the RPC.
func TestStopCancelsInFlightHeartbeatRPC(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

	// Simulate an RPC that blocks until its context is cancelled.
	heartbeatStarted := make(chan struct{})
	mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *workflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*workflowservice.RecordWorkerHeartbeatResponse, error) {
			close(heartbeatStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		}).AnyTimes()

	wfClient := NewServiceClient(mockService, nil, ClientOptions{})

	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	hw := &sharedNamespaceWorker{
		client:          wfClient,
		namespace:       "test-ns",
		interval:        50 * time.Millisecond,
		heartbeatCtx:    heartbeatCtx,
		heartbeatCancel: heartbeatCancel,
		callbacks: map[string]func() *workerpb.WorkerHeartbeat{
			"worker1": func() *workerpb.WorkerHeartbeat { return &workerpb.WorkerHeartbeat{} },
		},
		stopC:    make(chan struct{}),
		stoppedC: make(chan struct{}),
		logger:   ilog.NewDefaultLogger(),
	}
	hw.started.Store(true)
	go hw.run()

	// Wait for the heartbeat RPC to be in-flight.
	select {
	case <-heartbeatStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for heartbeat RPC to start")
	}

	// stop() should return promptly because heartbeatCancel() unblocks the
	// in-flight RPC. Without the fix, this hangs forever.
	done := make(chan struct{})
	go func() {
		hw.stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stop() — in-flight heartbeat RPC was not cancelled")
	}
}

func TestWorkerCommandPollUsesWorkerCommandsQueue(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	const (
		namespace    = "test-ns"
		controlQueue = "temporal-sys/worker-commands/test-ns/grouping-key"
		workerKey    = "worker-command-worker"
		workerIdent  = "worker-identity"
	)

	mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
			if req.Namespace != namespace {
				t.Fatalf("namespace = %q, want %q", req.Namespace, namespace)
			}
			if req.Identity != workerIdent {
				t.Fatalf("identity = %q, want %q", req.Identity, workerIdent)
			}
			if req.WorkerInstanceKey != workerKey {
				t.Fatalf("worker instance key = %q, want %q", req.WorkerInstanceKey, workerKey)
			}
			if req.TaskQueue.GetName() != controlQueue {
				t.Fatalf("task queue = %q, want %q", req.TaskQueue.GetName(), controlQueue)
			}
			if req.TaskQueue.GetKind() != enumspb.TASK_QUEUE_KIND_WORKER_COMMANDS {
				t.Fatalf("task queue kind = %v, want worker commands", req.TaskQueue.GetKind())
			}
			return &workflowservice.PollNexusTaskQueueResponse{}, nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hw := &sharedNamespaceWorker{
		client: &WorkflowClient{
			workflowService: mockService,
			identity:        workerIdent,
		},
		namespace:              namespace,
		heartbeatCtx:           ctx,
		workerControlTaskQueue: controlQueue,
		workerInstanceKey:      workerKey,
		metricsHandler:         metrics.NopHandler,
	}

	if _, err := hw.pollWorkerCommandTask(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCommandCancelActivity(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	activityTaskToken := []byte{1, 2, 3, 4}
	execReqBytes, err := proto.Marshal(&workerservicepb.ExecuteCommandsRequest{
		Commands: []*workerpb.WorkerCommand{
			{
				Type: &workerpb.WorkerCommand_CancelActivity{
					CancelActivity: &workerpb.CancelActivityCommand{TaskToken: activityTaskToken},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mockService.EXPECT().RespondNexusTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondNexusTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondNexusTaskCompletedResponse, error) {
			if req.Namespace != "test-ns" {
				t.Fatalf("namespace = %q, want test-ns", req.Namespace)
			}
			if !bytes.Equal(req.TaskToken, []byte{9, 9, 9}) {
				t.Fatalf("task token = %v, want [9 9 9]", req.TaskToken)
			}
			if req.PollerGroupId != "poller-group" {
				t.Fatalf("poller group = %q, want poller-group", req.PollerGroupId)
			}
			var execResp workerservicepb.ExecuteCommandsResponse
			if err := proto.Unmarshal(req.GetResponse().GetStartOperation().GetSyncSuccess().GetPayload().GetData(), &execResp); err != nil {
				t.Fatal(err)
			}
			if len(execResp.GetResults()) != 1 || execResp.GetResults()[0].GetCancelActivity() == nil {
				t.Fatalf("unexpected worker command results: %v", execResp.GetResults())
			}
			return &workflowservice.RespondNexusTaskCompletedResponse{}, nil
		})

	activityCancellationCallbacks := newActivityCancellationCallbacks()
	activityCtx, activityCancel := context.WithCancelCause(context.Background())
	defer activityCancel(nil)
	unregisterActivity := activityCancellationCallbacks.register(activityTaskToken, activityCancel)
	defer unregisterActivity()
	hw := &sharedNamespaceWorker{
		client: &WorkflowClient{
			workflowService: mockService,
			identity:        "worker-identity",
		},
		namespace:                     "test-ns",
		metricsHandler:                metrics.NopHandler,
		activityCancellationCallbacks: activityCancellationCallbacks,
	}

	err = hw.handleWorkerCommandTask(&workflowservice.PollNexusTaskQueueResponse{
		TaskToken:     []byte{9, 9, 9},
		PollerGroupId: "poller-group",
		Request: &nexuspb.Request{
			Variant: &nexuspb.Request_StartOperation{
				StartOperation: &nexuspb.StartOperationRequest{
					Payload: &commonpb.Payload{Data: execReqBytes},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !IsCanceledError(context.Cause(activityCtx)) {
		t.Fatalf("activity context cause = %v, want canceled error", context.Cause(activityCtx))
	}
}

func TestWorkerCommandsDisabledDoesNotPoll(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordWorkerHeartbeatResponse{}, nil).AnyTimes()
	wfClient := NewServiceClient(mockService, nil, ClientOptions{
		Namespace: "test-ns",
		Identity:  "worker-identity",
	})

	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	hw := &sharedNamespaceWorker{
		client:                  wfClient,
		namespace:               "test-ns",
		interval:                10 * time.Millisecond,
		heartbeatCtx:            heartbeatCtx,
		heartbeatCancel:         heartbeatCancel,
		callbacks:               map[string]func() *workerpb.WorkerHeartbeat{"worker1": func() *workerpb.WorkerHeartbeat { return &workerpb.WorkerHeartbeat{} }},
		workerCommandsSupported: false,
		workerControlTaskQueue:  "temporal-sys/worker-commands/test-ns/grouping-key",
		workerInstanceKey:       "worker-command-worker",
		metricsHandler:          metrics.NopHandler,
		stopC:                   make(chan struct{}),
		stoppedC:                make(chan struct{}),
		logger:                  ilog.NewDefaultLogger(),
	}
	hw.started.Store(true)
	go hw.run()
	time.Sleep(25 * time.Millisecond)
	hw.stop()
}
