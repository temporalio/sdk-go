package internal

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang/mock/gomock"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	workerservicepb "go.temporal.io/api/nexusservices/workerservice/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// TestStopCancelsInFlightHeartbeatRPC verifies that calling stop() on a
// sharedNamespaceWorker cancels an in-flight heartbeat RPC. Without the fix
// (using context.Background() for the RPC), stop() would hang forever because
// the blocked RPC prevents run() from seeing stopC. With the fix
// (heartbeatCtx), stop() cancels the context first, unblocking the RPC.
func TestStopCancelsInFlightHeartbeatRPC(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

		mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

		// Simulate an RPC that blocks until its context is cancelled.
		heartbeatStarted := false
		mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ *workflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*workflowservice.RecordWorkerHeartbeatResponse, error) {
				heartbeatStarted = true
				<-ctx.Done()
				return nil, ctx.Err()
			}).AnyTimes()

		wfClient := NewServiceClient(mockService, nil, ClientOptions{})

		heartbeatCtx, heartbeatCancel := context.WithCancel(t.Context())
		hw := &sharedNamespaceWorker{
			client:          wfClient,
			namespace:       "test-ns",
			interval:        50 * time.Millisecond,
			workerCtx:       heartbeatCtx,
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

		synctest.Wait()
		if !heartbeatStarted {
			t.Fatal("heartbeat RPC did not start")
		}

		// stop() should return because heartbeatCancel() unblocks the in-flight RPC.
		hw.stop()
	})
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
			if req.DeploymentOptions.GetBuildId() != "1.0" {
				t.Fatalf("build ID = %q, want %q", req.DeploymentOptions.GetBuildId(), "1.0")
			}
			if req.DeploymentOptions.GetWorkerVersioningMode() != enumspb.WORKER_VERSIONING_MODE_UNVERSIONED {
				t.Fatalf("worker versioning mode = %v, want unversioned", req.DeploymentOptions.GetWorkerVersioningMode())
			}
			return &workflowservice.PollNexusTaskQueueResponse{}, nil
		})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hw := &sharedNamespaceWorker{
		client: &WorkflowClient{
			workflowService: mockService,
			identity:        workerIdent,
		},
		namespace:              namespace,
		workerCtx:              ctx,
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
	activityCtx, activityCancel := context.WithCancelCause(t.Context())
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
	synctest.Test(t, func(t *testing.T) {
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

		heartbeatCtx, heartbeatCancel := context.WithCancel(t.Context())
		hw := &sharedNamespaceWorker{
			client:                  wfClient,
			namespace:               "test-ns",
			interval:                10 * time.Millisecond,
			workerCtx:               heartbeatCtx,
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
	})
}

func TestWorkerHeartbeatSendsImmediatelyWithIdentity(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

		mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

		var request *workflowservice.RecordWorkerHeartbeatRequest
		mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*workflowservice.RecordWorkerHeartbeatResponse, error) {
				request = req
				return &workflowservice.RecordWorkerHeartbeatResponse{}, nil
			}).AnyTimes()

		wfClient := NewServiceClient(mockService, nil, ClientOptions{
			Namespace:               "test-ns",
			Identity:                "test-client-identity",
			WorkerHeartbeatInterval: time.Minute,
		})
		wfClient.namespaceData = &namespaceData{
			capabilities: &namespacepb.NamespaceInfo_Capabilities{WorkerHeartbeats: true},
		}
		worker := NewAggregatedWorker(wfClient, "test-task-queue", WorkerOptions{})
		if err := worker.registerHeartbeatWorker(); err != nil {
			t.Fatal(err)
		}
		defer worker.unregisterHeartbeatWorker()

		synctest.Wait()
		if request == nil {
			t.Fatal("initial worker heartbeat was not sent")
		}
		if request.GetNamespace() != "test-ns" {
			t.Fatalf("namespace = %q, want test-ns", request.GetNamespace())
		}
		if request.GetIdentity() != "test-client-identity" {
			t.Fatalf("identity = %q, want test-client-identity", request.GetIdentity())
		}
		if len(request.GetWorkerHeartbeat()) != 1 {
			t.Fatalf("worker heartbeat count = %d, want 1", len(request.GetWorkerHeartbeat()))
		}
	})
}

func TestWorkerHeartbeatElapsedSinceLastHeartbeatUnsetOnInitialHeartbeat(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	wfClient := NewServiceClient(mockService, nil, ClientOptions{
		Identity:                "test-client-identity",
		WorkerHeartbeatInterval: time.Second,
	})

	worker := NewAggregatedWorker(wfClient, "test-task-queue", WorkerOptions{})
	if worker.heartbeatCallback == nil {
		t.Fatal("heartbeat callback is nil")
	}

	firstHeartbeat := worker.heartbeatCallback()
	if firstHeartbeat.GetElapsedSinceLastHeartbeat() != nil {
		t.Fatalf("initial elapsed since last heartbeat = %v, want nil", firstHeartbeat.GetElapsedSinceLastHeartbeat())
	}

	secondHeartbeat := worker.heartbeatCallback()
	if secondHeartbeat.GetElapsedSinceLastHeartbeat() == nil {
		t.Fatal("second elapsed since last heartbeat is nil, want set")
	}
}

func TestWorkerHeartbeatEnvironmentSentUntilAccepted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

		mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

		var requestsMu sync.Mutex
		var requests []*workflowservice.RecordWorkerHeartbeatRequest
		requestCount := func() int {
			requestsMu.Lock()
			defer requestsMu.Unlock()
			return len(requests)
		}
		mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*workflowservice.RecordWorkerHeartbeatResponse, error) {
				requestsMu.Lock()
				defer requestsMu.Unlock()
				requests = append(requests, req)
				// Fail the first delivery so the environment must be retried.
				if len(requests) == 1 {
					return nil, status.Error(codes.Unavailable, "heartbeat retry")
				}
				return &workflowservice.RecordWorkerHeartbeatResponse{}, nil
			}).AnyTimes()

		wfClient := NewServiceClient(mockService, nil, ClientOptions{
			Namespace:               "test-ns",
			Identity:                "test-client-identity",
			WorkerHeartbeatInterval: time.Second,
		})
		wfClient.namespaceData = &namespaceData{
			capabilities: &namespacepb.NamespaceInfo_Capabilities{WorkerHeartbeats: true},
		}
		worker := NewAggregatedWorker(wfClient, "test-task-queue", WorkerOptions{})
		if err := worker.registerHeartbeatWorker(); err != nil {
			t.Fatal(err)
		}
		defer worker.unregisterHeartbeatWorker()

		for requestCount() < 3 {
			synctest.Wait()
			time.Sleep(time.Second)
		}

		requestsMu.Lock()
		defer requestsMu.Unlock()
		for i, want := range []bool{true, true, false} {
			hb := requests[i].GetWorkerHeartbeat()[0]
			if got := hb.GetEnvironment() != nil; got != want {
				t.Fatalf("heartbeat %d environment present = %v, want %v", i, got, want)
			}
		}
		env := requests[0].GetWorkerHeartbeat()[0].GetEnvironment()
		if len(env.GetRuntimes()) != 1 || env.GetRuntimes()[0].GetType() != workerpb.EnvironmentInfo_Runtime_RUNTIME_TYPE_GO {
			t.Fatalf("environment runtimes = %v, want a single GO runtime", env.GetRuntimes())
		}
	})
}

func TestWorkerHeartbeatEnvironmentDisabled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

		mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

		var request *workflowservice.RecordWorkerHeartbeatRequest
		mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*workflowservice.RecordWorkerHeartbeatResponse, error) {
				request = req
				return &workflowservice.RecordWorkerHeartbeatResponse{}, nil
			}).AnyTimes()

		wfClient := NewServiceClient(mockService, nil, ClientOptions{
			Namespace:                    "test-ns",
			WorkerHeartbeatInterval:      time.Minute,
			DisableWorkerEnvironmentInfo: true,
		})
		wfClient.namespaceData = &namespaceData{
			capabilities: &namespacepb.NamespaceInfo_Capabilities{WorkerHeartbeats: true},
		}
		worker := NewAggregatedWorker(wfClient, "test-task-queue", WorkerOptions{})
		if err := worker.registerHeartbeatWorker(); err != nil {
			t.Fatal(err)
		}
		defer worker.unregisterHeartbeatWorker()

		synctest.Wait()
		if request == nil {
			t.Fatal("initial worker heartbeat was not sent")
		}
		if env := request.GetWorkerHeartbeat()[0].GetEnvironment(); env != nil {
			t.Fatalf("environment = %v, want nil when disabled", env)
		}
	})
}

func TestWorkerHeartbeatEnvironmentIncludedInShutdownHeartbeat(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

	wfClient := NewServiceClient(mockService, nil, ClientOptions{
		Namespace:               "test-ns",
		WorkerHeartbeatInterval: time.Minute,
	})
	worker := NewAggregatedWorker(wfClient, "test-task-queue", WorkerOptions{})

	// Without any accepted periodic heartbeat, the heartbeat built for ShutdownWorker must
	// still carry the environment.
	if worker.heartbeatCallback().GetEnvironment() == nil {
		t.Fatal("heartbeat before any success has no environment, want environment")
	}
	worker.heartbeatSuccess()
	if env := worker.heartbeatCallback().GetEnvironment(); env != nil {
		t.Fatalf("heartbeat after success has environment %v, want nil", env)
	}
}
