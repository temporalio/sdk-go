package internal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	workerservicepb "go.temporal.io/api/nexusservices/workerservice/v1"
	querypb "go.temporal.io/api/query/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWorkflowTaskHeartbeatNotificationRouting(t *testing.T) {
	r := &workflowTaskHeartbeatNotifications{}
	first, retireFirst := r.register([]byte("first"))
	second, retireSecond := r.register([]byte("second"))
	defer retireSecond()
	require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify([]byte("unknown")))
	require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, r.notify([]byte("first")))
	<-first
	require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_ALREADY_RECEIVED, r.notify([]byte("first")))
	select {
	case <-first:
		t.Fatal("duplicate notification was delivered")
	case <-second:
		t.Fatal("notification reached another task")
	default:
	}
	retireFirst()
	require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify([]byte("first")))
	replacement, retireReplacement := r.register([]byte("first"))
	defer retireReplacement()
	retireFirst()
	require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, r.notify([]byte("first")))
	<-replacement
	retireReplacement()
	retireReplacement()
	retireSecond()
	require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, r.notify([]byte("first")))
}

func TestWorkflowTaskHeartbeatNotificationRetirementRace(t *testing.T) {
	r := &workflowTaskHeartbeatNotifications{}
	for range 100 {
		_, retire := r.register([]byte("task"))
		var wg sync.WaitGroup
		wg.Go(func() { r.notify([]byte("task")) })
		wg.Go(retire)
		wg.Wait()
		require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, r.notify([]byte("task")))
	}
}

func TestWorkerCommandWorkflowTaskHeartbeatNotificationBatch(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	r := &workflowTaskHeartbeatNotifications{}
	ready, retire := r.register([]byte("active"))
	defer retire()
	cancellations := newActivityCancellationCallbacks()
	activityCtx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	retireActivity := cancellations.register([]byte("activity"), cancel)
	defer retireActivity()
	commands := []*workerpb.WorkerCommand{}
	for _, token := range [][]byte{[]byte("active"), []byte("active"), []byte("missing"), nil} {
		commands = append(commands, &workerpb.WorkerCommand{Type: &workerpb.WorkerCommand_WorkflowTaskHeartbeatNotification{
			WorkflowTaskHeartbeatNotification: &workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: token},
		}})
	}
	commands = append(commands, &workerpb.WorkerCommand{Type: &workerpb.WorkerCommand_CancelActivity{
		CancelActivity: &workerpb.CancelActivityCommand{TaskToken: []byte("activity")},
	}}, &workerpb.WorkerCommand{})
	service.EXPECT().RespondNexusTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondNexusTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondNexusTaskCompletedResponse, error) {
			require.Equal(t, []byte("control-task"), req.TaskToken)
			var response workerservicepb.ExecuteCommandsResponse
			require.NoError(t, proto.Unmarshal(req.Response.GetStartOperation().GetSyncSuccess().GetPayload().GetData(), &response))
			require.Len(t, response.Results, len(commands))
			for i, outcome := range []workerpb.WorkflowTaskHeartbeatNotificationResult_Outcome{
				workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED,
				workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_ALREADY_RECEIVED,
				workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH,
				workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH,
			} {
				require.Equal(t, outcome, response.Results[i].GetWorkflowTaskHeartbeatNotification().GetOutcome())
			}
			require.NotNil(t, response.Results[4].GetCancelActivity())
			require.Nil(t, response.Results[5].GetType())
			return &workflowservice.RespondNexusTaskCompletedResponse{}, nil
		})
	worker := &sharedNamespaceWorker{
		client: &WorkflowClient{workflowService: service}, logger: ilog.NewNopLogger(), metricsHandler: metrics.NopHandler,
		activityCancellationCallbacks: cancellations, workflowTaskHeartbeatNotifications: r,
	}
	payload, err := proto.Marshal(&workerservicepb.ExecuteCommandsRequest{Commands: commands})
	require.NoError(t, err)
	require.NoError(t, worker.handleWorkerCommandTask(&workflowservice.PollNexusTaskQueueResponse{
		TaskToken: []byte("control-task"), Request: &nexuspb.Request{Variant: &nexuspb.Request_StartOperation{
			StartOperation: &nexuspb.StartOperationRequest{Payload: &commonpb.Payload{Data: payload}},
		}},
	}))
	<-ready
	select {
	case <-ready:
		t.Fatal("duplicate notification was delivered")
	default:
	}
	require.True(t, IsCanceledError(context.Cause(activityCtx)))
}

func TestWorkflowTaskHeartbeatNotificationCommandWorkerExit(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		heartbeatError error
	}{
		{name: "normal stop"},
		{name: "transient error", heartbeatError: status.Error(codes.Unavailable, "unavailable")},
		{name: "unsupported server", heartbeatError: status.Error(codes.Unimplemented, "unsupported")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
				service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
				service.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RecordWorkerHeartbeatResponse{}, scenario.heartbeatError).AnyTimes()
				service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, _ *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
						<-ctx.Done()
						return nil, ctx.Err()
					}).AnyTimes()
				client := NewServiceClient(service, nil, ClientOptions{})
				worker := client.heartbeatManager.sharedNamespaceWorkerFor(client.namespace)
				worker.callbacks["worker"] = func() *workerpb.WorkerHeartbeat { return &workerpb.WorkerHeartbeat{} }
				worker.workerCommandsSupported = true
				worker.workflowTaskHeartbeatNotifications.commandWorkerEnabled.Store(true)
				worker.started.Store(true)
				processor := &workflowTaskProcessor{workflowTaskHeartbeatNotifications: worker.workflowTaskHeartbeatNotifications}
				processor.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
				poller := processor.createPoller(NonSticky, nil)
				go worker.run()
				synctest.Wait()
				if status.Code(scenario.heartbeatError) == codes.Unimplemented {
					<-worker.stoppedC
				} else {
					require.True(t, poller.getNextPollRequest().SupportsWorkflowTaskHeartbeatNotification)
				}
				worker.stop()
				require.False(t, poller.getNextPollRequest().SupportsWorkflowTaskHeartbeatNotification)
			})
		})
	}
}

func TestWorkflowTaskHeartbeatNotificationAvailability(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		heartbeats bool
		commands   bool
		disabled   bool
	}{
		{name: "supported", heartbeats: true, commands: true},
		{name: "no worker commands", heartbeats: true},
		{name: "no worker heartbeats"},
		{name: "disabled client", heartbeats: true, commands: true, disabled: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
				service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
				service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.DescribeNamespaceResponse{
					NamespaceInfo: &namespacepb.NamespaceInfo{Capabilities: &namespacepb.NamespaceInfo_Capabilities{
						WorkerHeartbeats: scenario.heartbeats, WorkerCommands: scenario.commands,
					}},
				}, nil).AnyTimes()
				service.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RecordWorkerHeartbeatResponse{}, nil).AnyTimes()
				poll := service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, _ *workflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollNexusTaskQueueResponse, error) {
						<-ctx.Done()
						return nil, ctx.Err()
					})
				available := scenario.heartbeats && scenario.commands && !scenario.disabled
				if available {
					poll.MinTimes(1)
				} else {
					poll.Times(0)
				}
				options := ClientOptions{}
				if scenario.disabled {
					options.WorkerHeartbeatInterval = -1
				}
				client := NewServiceClient(service, nil, options)
				worker := NewAggregatedWorker(client, testWorkflowTaskTaskqueue, WorkerOptions{})
				t.Cleanup(worker.cacheLease.release)
				require.NoError(t, worker.registerHeartbeatWorker())
				synctest.Wait()
				for _, mode := range []workflowTaskPollerMode{NonSticky, Sticky} {
					poller := worker.workflowWorker.taskProcessor.createPoller(mode, nil)
					request := poller.getNextPollRequest()
					if mode == Sticky {
						require.Equal(t, enumspb.TASK_QUEUE_KIND_STICKY, request.TaskQueue.Kind)
					}
					require.Equal(t, available, request.SupportsWorkflowTaskHeartbeatNotification)
				}
				client.eagerDispatcher.registerWorker(worker.workflowWorker)
				request := &workflowservice.StartWorkflowExecutionRequest{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}
				executor := client.eagerDispatcher.applyToRequest(request)
				require.NotNil(t, executor)
				executor.releaseUnused()
				if available {
					require.Equal(t, worker.executionParams.workerControlTaskQueue, request.WorkerControlTaskQueue)
				} else {
					require.Empty(t, request.WorkerControlTaskQueue)
				}
				worker.unregisterHeartbeatWorker()
			})
		})
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowTaskHeartbeatNotificationQueries() {
	for _, standalone := range []bool{true, false} {
		s.Run(map[bool]string{true: "standalone", false: "on normal WFT"}[standalone], func() {
			r := &workflowTaskHeartbeatNotifications{}
			token := []byte("query-task")
			var outcome workerpb.WorkflowTaskHeartbeatNotificationResult_Outcome
			s.registry.RegisterWorkflowWithOptions(func(ctx Context) error {
				if err := SetQueryHandler(ctx, "status", func() (string, error) {
					outcome = r.notify(token)
					return "ready", nil
				}); err != nil {
					return err
				}
				return Await(ctx, func() bool { return false })
			}, RegisterWorkflowOptions{Name: "WHNQuery", DisableAlreadyRegisteredCheck: true})
			params := s.getTestWorkerExecutionParams()
			params.workflowTaskHeartbeatNotifications = r
			handler := newWorkflowTaskHandler(params, nil, s.registry)
			task := &workflowTask{task: createWorkflowTask([]*historypb.HistoryEvent{
				createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
					TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
				}),
			}, 0, "WHNQuery")}
			task.task.TaskToken = token
			query := &querypb.WorkflowQuery{QueryType: "status"}
			if standalone {
				task.task.Query = query
			} else {
				task.task.Queries = map[string]*querypb.WorkflowQuery{"query": query}
			}
			wfctx := s.mustWorkflowContextImpl(task, handler)
			response, err := handler.ProcessWorkflowTask(task, wfctx, func(*workflowTaskCompletion, time.Time) (*workflowTask, error) {
				s.Fail("query caused a heartbeat without a Local Activity")
				return nil, nil
			})
			wfctx.Unlock(err)
			s.Require().NoError(err)
			s.Require().NotNil(response)
			if standalone {
				s.Equal(workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, outcome)
				s.Equal(enumspb.QUERY_RESULT_TYPE_ANSWERED, response.rawRequest.(*workflowservice.RespondQueryTaskCompletedRequest).CompletedType)
			} else {
				s.Equal(workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, outcome)
				s.Equal(enumspb.QUERY_RESULT_TYPE_ANSWERED, response.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest).QueryResults["query"].ResultType)
			}
			s.Equal(workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, r.notify(token))
		})
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowTaskHeartbeatNotificationSafeYield() {
	for _, scenario := range []struct {
		name           string
		localActivity  bool
		heartbeatError error
		timedHeartbeat bool
		stopWorker     bool
		panicWorkflow  bool
	}{
		{name: "normal completion"},
		{name: "pending local activity", localActivity: true},
		{name: "heartbeat failure", localActivity: true, heartbeatError: errors.New("heartbeat failed")},
		{name: "timed fallback", localActivity: true, timedHeartbeat: true},
		{name: "worker shutdown", localActivity: true, stopWorker: true},
		{name: "workflow panic", panicWorkflow: true},
	} {
		s.Run(scenario.name, func() {
			synctest.Test(s.T(), func(t *testing.T) {
				r := &workflowTaskHeartbeatNotifications{}
				started, yield := make(chan struct{}), make(chan struct{})
				s.registry.RegisterWorkflowWithOptions(func(ctx Context) error {
					close(started)
					<-yield
					if scenario.panicWorkflow {
						panic("workflow failed")
					}
					if scenario.localActivity {
						ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})
						return ExecuteLocalActivity(ctx, func() error { return nil }).Get(ctx, nil)
					}
					return nil
				}, RegisterWorkflowOptions{Name: "WHNSafeYield", DisableAlreadyRegisteredCheck: true})
				params := s.getTestWorkerExecutionParams()
				var cacheLock sync.Mutex
				var lease *workerCacheLease
				params.cache, lease = newWorkerCache(&sharedWorkerCache{}, &cacheLock, defaultStickyCacheSize)
				t.Cleanup(lease.release)
				params.workflowTaskHeartbeatNotifications = r
				handler := newWorkflowTaskHandler(params, nil, s.registry).(*workflowTaskHandlerImpl)
				stop := make(chan struct{})
				handler.laTunnel = newLocalActivityTunnel(stop)
				task := &workflowTask{task: createWorkflowTask([]*historypb.HistoryEvent{
					createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
						WorkflowTaskTimeout: durationpb.New(10 * time.Second),
						TaskQueue:           &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
					}),
				}, 0, "WHNSafeYield")}
				task.task.TaskToken = []byte("executing")
				wfctx, err := handler.GetOrCreateWorkflowContext(task.task, nil)
				require.NoError(t, err)
				done := make(chan struct{})
				var result *workflowTaskCompletion
				var taskErr error
				heartbeats := 0
				go func() {
					defer close(done)
					result, taskErr = handler.ProcessWorkflowTask(task, wfctx, func(completion *workflowTaskCompletion, start time.Time) (*workflowTask, error) {
						heartbeats++
						if scenario.timedHeartbeat {
							require.Equal(t, 8*time.Second, time.Since(start))
						} else {
							require.Less(t, time.Since(start), 8*time.Second)
						}
						require.True(t, completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest).ForceCreateNewWorkflowTask)
						return nil, scenario.heartbeatError
					})
					wfctx.Unlock(taskErr)
				}()
				<-started
				if !scenario.timedHeartbeat {
					require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, r.notify(task.task.TaskToken))
					require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_ALREADY_RECEIVED, r.notify(task.task.TaskToken))
				}
				synctest.Wait()
				require.Zero(t, heartbeats)
				if scenario.stopWorker {
					close(stop)
				}
				close(yield)
				<-done
				if scenario.localActivity {
					if scenario.stopWorker {
						require.Zero(t, heartbeats)
					} else {
						require.Equal(t, 1, heartbeats)
					}
					require.Nil(t, result)
				} else if !scenario.panicWorkflow {
					require.Zero(t, heartbeats)
					require.NotNil(t, result)
				}
				if scenario.heartbeatError != nil {
					require.ErrorContains(t, taskErr, scenario.heartbeatError.Error())
				} else if scenario.panicWorkflow {
					require.ErrorContains(t, taskErr, "workflow failed")
				} else {
					require.NoError(t, taskErr)
				}
				require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, r.notify(task.task.TaskToken))
			})
		})
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowTaskHeartbeatNotificationReplacement() {
	synctest.Test(s.T(), func(t *testing.T) {
		r := &workflowTaskHeartbeatNotifications{}
		firstToken, nextToken := []byte("first"), []byte("next")
		var signalSeen bool
		s.registry.RegisterWorkflowWithOptions(func(ctx Context) error {
			ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})
			ExecuteLocalActivity(ctx, func() error { return nil })
			require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, r.notify(firstToken))
			GetSignalChannel(ctx, "interrupt").Receive(ctx, nil)
			signalSeen = true
			require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify(firstToken))
			require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, r.notify(nextToken))
			return Await(ctx, func() bool { return false })
		}, RegisterWorkflowOptions{Name: "WHNReplacement"})
		params := s.getTestWorkerExecutionParams()
		var cacheLock sync.Mutex
		var lease *workerCacheLease
		params.cache, lease = newWorkerCache(&sharedWorkerCache{}, &cacheLock, defaultStickyCacheSize)
		t.Cleanup(lease.release)
		params.workflowTaskHeartbeatNotifications = r
		handler := newWorkflowTaskHandler(params, nil, s.registry).(*workflowTaskHandlerImpl)
		handler.laTunnel = newLocalActivityTunnel(make(chan struct{}))
		first := &workflowTask{task: createWorkflowTask([]*historypb.HistoryEvent{
			createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
				WorkflowTaskTimeout: durationpb.New(10 * time.Second),
				TaskQueue:           &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
			}),
		}, 0, "WHNReplacement")}
		first.task.TaskToken = firstToken
		first.task.StartedEventId = 3
		next := &workflowTask{task: createWorkflowTaskWithQueries([]*historypb.HistoryEvent{
			createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2, StartedEventId: 3}),
			createTestEventWorkflowExecutionSignaled(5, "interrupt"),
			createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{}),
			createTestEventWorkflowTaskStarted(7),
		}, 3, "WHNReplacement", nil, false)}
		next.task.WorkflowExecution = first.task.WorkflowExecution
		next.task.TaskToken = nextToken
		next.task.StartedEventId = 7
		wfctx, err := handler.GetOrCreateWorkflowContext(first.task, nil)
		require.NoError(t, err)
		heartbeats := 0
		result, err := handler.ProcessWorkflowTask(first, wfctx, func(completion *workflowTaskCompletion, start time.Time) (*workflowTask, error) {
			heartbeats++
			require.Less(t, time.Since(start), 8*time.Second)
			require.True(t, completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest).ForceCreateNewWorkflowTask)
			if heartbeats == 1 {
				require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, r.notify(firstToken))
				return next, nil
			}
			return nil, nil
		})
		wfctx.Unlock(err)
		require.NoError(t, err)
		require.Nil(t, result)
		require.True(t, signalSeen)
		require.Equal(t, 2, heartbeats)
		require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, r.notify(nextToken))
	})
}

func TestWorkflowTaskHeartbeatNotificationRPC(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		eager   bool
		noCache bool
	}{
		{name: "polled task"},
		{name: "eager task", eager: true},
		{name: "cache disabled", noCache: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
				client := NewServiceClient(service, nil, ClientOptions{Namespace: testNamespace})
				worker := NewAggregatedWorker(client, testWorkflowTaskTaskqueue, WorkerOptions{})
				t.Cleanup(worker.cacheLease.release)
				if scenario.noCache {
					worker.cacheLease.workerCache.maxWorkflowCacheSize = 0
				}
				shared := client.heartbeatManager.sharedNamespaceWorkerFor(testNamespace)
				activityStarted, finishActivity := make(chan struct{}), make(chan struct{})
				signalSeen := make(chan struct{})
				worker.RegisterWorkflowWithOptions(func(ctx Context) error {
					ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})
					future := ExecuteLocalActivity(ctx, func() error {
						close(activityStarted)
						<-finishActivity
						return nil
					})
					GetSignalChannel(ctx, "interrupt").Receive(ctx, nil)
					close(signalSeen)
					return future.Get(ctx, nil)
				}, RegisterWorkflowOptions{Name: "WHNRPC"})
				first := createWorkflowTask([]*historypb.HistoryEvent{
					createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
						WorkflowTaskTimeout: durationpb.New(10 * time.Second),
						TaskQueue:           &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
					}),
				}, 0, "WHNRPC")
				first.TaskToken, first.StartedEventId = []byte("first"), 3
				next := createWorkflowTaskWithQueries([]*historypb.HistoryEvent{
					createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2, StartedEventId: 3}),
					createTestEventWorkflowExecutionSignaled(5, "interrupt"),
					createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{}),
					createTestEventWorkflowTaskStarted(7),
				}, 3, "WHNRPC", nil, false)
				next.TaskToken, next.StartedEventId = []byte("next"), 7
				next.WorkflowExecution = first.WorkflowExecution
				start := time.Now()
				gomock.InOrder(
					service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
						DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
							require.Equal(t, first.TaskToken, req.TaskToken)
							require.True(t, req.ForceCreateNewWorkflowTask)
							require.True(t, req.ReturnNewWorkflowTask)
							require.Less(t, time.Since(start), 8*time.Second)
							return &workflowservice.RespondWorkflowTaskCompletedResponse{WorkflowTask: next}, nil
						}),
					service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
						DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
							require.Equal(t, next.TaskToken, req.TaskToken)
							require.False(t, req.ForceCreateNewWorkflowTask)
							require.Equal(t, enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, req.Commands[len(req.Commands)-1].CommandType)
							return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
						}),
				)
				service.EXPECT().RespondNexusTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, req *workflowservice.RespondNexusTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondNexusTaskCompletedResponse, error) {
						var response workerservicepb.ExecuteCommandsResponse
						require.NoError(t, proto.Unmarshal(req.Response.GetStartOperation().GetSyncSuccess().GetPayload().GetData(), &response))
						require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_HANDLED, response.Results[0].GetWorkflowTaskHeartbeatNotification().GetOutcome())
						return &workflowservice.RespondNexusTaskCompletedResponse{}, nil
					})
				processor := worker.workflowWorker.taskProcessor
				workflowDone, activityDone := make(chan error, 1), make(chan error, 1)
				go func() {
					if scenario.eager {
						workflowDone <- processor.ProcessTask(&eagerWorkflowTask{task: first})
					} else {
						workflowDone <- processor.ProcessTask(&workflowTask{task: first})
					}
				}()
				go func() {
					poller := worker.workflowWorker.localActivityWorker.options.taskProcessor.(*localActivityTaskPoller)
					task, err := poller.PollTask(pollerGroupLease{})
					if err != nil {
						activityDone <- err
						return
					}
					activityDone <- poller.ProcessTask(task)
				}()
				<-activityStarted
				synctest.Wait()
				payload, err := proto.Marshal(&workerservicepb.ExecuteCommandsRequest{
					Commands: []*workerpb.WorkerCommand{{Type: &workerpb.WorkerCommand_WorkflowTaskHeartbeatNotification{
						WorkflowTaskHeartbeatNotification: &workerpb.WorkflowTaskHeartbeatNotificationCommand{TaskToken: first.TaskToken},
					}}},
				})
				require.NoError(t, err)
				require.NoError(t, shared.handleWorkerCommandTask(&workflowservice.PollNexusTaskQueueResponse{
					Request: &nexuspb.Request{Variant: &nexuspb.Request_StartOperation{
						StartOperation: &nexuspb.StartOperationRequest{Payload: &commonpb.Payload{Data: payload}},
					}},
				}))
				<-signalSeen
				select {
				case <-activityDone:
					t.Fatal("Local Activity finished before the signal was delivered")
				default:
				}
				close(finishActivity)
				require.NoError(t, <-activityDone)
				require.NoError(t, <-workflowDone)
				require.Equal(t, workerpb.WorkflowTaskHeartbeatNotificationResult_OUTCOME_NO_WORKFLOW_TASK, shared.workflowTaskHeartbeatNotifications.notify(next.TaskToken))
			})
		})
	}
}
