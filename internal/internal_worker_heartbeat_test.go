package internal

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	workerservicepb "go.temporal.io/api/nexusservices/workerservice/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
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
		namespace      = "test-ns"
		groupingKey    = "grouping-key"
		controlQueue   = "temporal-sys/worker-commands/test-ns/grouping-key"
		workerIdentity = "worker-identity"
	)
	var workerInstanceKey string

	mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
			if req.Namespace != namespace {
				t.Fatalf("namespace = %q, want %q", req.Namespace, namespace)
			}
			if req.Identity != workerIdentity {
				t.Fatalf("identity = %q, want %q", req.Identity, workerIdentity)
			}
			if req.WorkerInstanceKey != workerInstanceKey {
				t.Fatalf("worker instance key = %q, want %q", req.WorkerInstanceKey, workerInstanceKey)
			}
			if req.TaskQueue.GetName() != controlQueue {
				t.Fatalf("task queue = %q, want %q", req.TaskQueue.GetName(), controlQueue)
			}
			if req.TaskQueue.GetKind() != enumspb.TASK_QUEUE_KIND_WORKER_COMMANDS {
				t.Fatalf("task queue kind = %v, want worker commands", req.TaskQueue.GetKind())
			}
			return &workflowservice.PollNexusTaskQueueResponse{}, nil
		})

	client := NewServiceClient(mockService, nil, ClientOptions{Namespace: namespace, Identity: workerIdentity})
	client.workerGroupingKey = groupingKey
	hw := client.heartbeatManager.sharedNamespaceWorkerFor(namespace)
	defer hw.heartbeatCancel()
	workerInstanceKey = hw.workerInstanceKey
	require.NotEmpty(t, workerInstanceKey)

	if _, err := hw.pollWorkerCommandTask(""); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCommandFirstPollUsesDescribeNamespacePollerGroups(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	const (
		namespace = "test-ns"
		groupID   = "described-poller-group"
	)

	mockService.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeNamespaceResponse{
			NamespaceInfo:    &namespacepb.NamespaceInfo{},
			PollerGroupsInfo: testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: groupID, Weight: 1}}),
		}, nil)
	mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
			require.Equal(t, groupID, req.GetPollerGroupId())
			return &workflowservice.PollNexusTaskQueueResponse{}, nil
		})

	client := NewServiceClient(mockService, nil, ClientOptions{Namespace: namespace})
	_, err := client.loadNamespaceData(metrics.NopHandler)
	require.NoError(t, err)

	hw := client.heartbeatManager.sharedNamespaceWorkerFor(namespace)
	defer hw.heartbeatCancel()
	_, err = hw.pollWorkerCommand()
	require.NoError(t, err)
}

func TestWorkerCommandPollUsesPollerGroups(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	const groupID = "poller-group"

	gomock.InOrder(
		mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
				if req.GetPollerGroupId() != "" {
					t.Fatalf("first poller group = %q, want empty", req.GetPollerGroupId())
				}
				return &workflowservice.PollNexusTaskQueueResponse{
					PollerGroupsInfo: testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: groupID, Weight: 1}}),
				}, nil
			}),
		mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
				if req.GetPollerGroupId() != groupID {
					t.Fatalf("second poller group = %q, want %q", req.GetPollerGroupId(), groupID)
				}
				return &workflowservice.PollNexusTaskQueueResponse{}, nil
			}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hw := &sharedNamespaceWorker{
		client: &WorkflowClient{
			workflowService: mockService,
		},
		workerCtx:              ctx,
		workerControlTaskQueue: "worker-commands",
		metricsHandler:         metrics.NopHandler,
		pollerGroups:           newPollerGroupManager(false, nil),
	}

	if _, err := hw.pollWorkerCommand(); err != nil {
		t.Fatal(err)
	}
	if _, err := hw.pollWorkerCommand(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCommandPollUsesExternalPollerGroupUpdate(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	const groupID = "external-poller-group"

	mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
			if req.GetPollerGroupId() != groupID {
				t.Fatalf("worker command poller group = %q, want %q", req.GetPollerGroupId(), groupID)
			}
			return &workflowservice.PollNexusTaskQueueResponse{
				PollerGroupsInfo: testPollerGroupsInfo(2, []*taskqueuepb.PollerGroupInfo{{Id: groupID, Weight: 1}}),
			}, nil
		})

	client := NewServiceClient(mockService, nil, ClientOptions{Namespace: "test-ns"})
	hw := client.heartbeatManager.sharedNamespaceWorkerFor(client.namespace)
	defer hw.heartbeatCancel()

	externalPollerGroups := newPollerGroupManager(false, client.pollerGroupInfoStore)
	externalPollerGroups.updateGroups(testPollerGroupsInfo(1, []*taskqueuepb.PollerGroupInfo{{Id: groupID, Weight: 1}}))
	require.Same(t, externalPollerGroups.groupInfos, hw.pollerGroups.groupInfos)

	if _, err := hw.pollWorkerCommand(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCommandsMaintainPollerGroupCoverage(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	groups := []*taskqueuepb.PollerGroupInfo{
		{Id: "group-a", Weight: 1},
		{Id: "group-b", Weight: 1},
	}
	started := make(chan string, len(groups))
	var pollCount atomic.Int32

	mockService.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, req *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
			if pollCount.Add(1) == 1 {
				if req.GetPollerGroupId() != groups[0].GetId() {
					t.Errorf("initial poller group = %q, want %q", req.GetPollerGroupId(), groups[0].GetId())
				}
				return &workflowservice.PollNexusTaskQueueResponse{
					PollerGroupsInfo: testPollerGroupsInfo(2, groups),
				}, nil
			}
			select {
			case started <- req.GetPollerGroupId():
			default:
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}).AnyTimes()

	ctx, cancel := context.WithCancel(context.Background())
	pollerGroups := newPollerGroupManager(false, nil)
	pollerGroups.updateGroups(testPollerGroupsInfo(1, groups[:1]))
	hw := &sharedNamespaceWorker{
		client: &WorkflowClient{
			workflowService: mockService,
		},
		workerCtx:              ctx,
		workerControlTaskQueue: "worker-commands",
		metricsHandler:         metrics.NopHandler,
		logger:                 ilog.NewNopLogger(),
		pollerGroups:           pollerGroups,
	}
	done := make(chan struct{})
	go func() {
		hw.runWorkerCommands()
		close(done)
	}()

	seen := make(map[string]struct{}, len(groups))
	for len(seen) < len(groups) {
		select {
		case groupID := <-started:
			seen[groupID] = struct{}{}
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatalf("timed out waiting for group coverage; saw %v", seen)
		}
	}
	for _, group := range groups {
		if _, ok := seen[group.GetId()]; !ok {
			cancel()
			<-done
			t.Fatalf("missing pending poll for group %q; saw %v", group.GetId(), seen)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out stopping worker command pollers")
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
}
