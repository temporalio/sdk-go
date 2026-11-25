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
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWorkflowHeartbeatNotificationRouting(t *testing.T) {
	r := &workflowTaskHeartbeatNotifications{}
	execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
	first, retireFirst := r.register([]byte("first"), execution)
	second, retireSecond := r.register([]byte("second"), &commonpb.WorkflowExecution{WorkflowId: "other-workflow", RunId: "other-run"})
	defer retireSecond()
	mismatch := r.notify([]byte("unknown"), execution)
	require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, mismatch.Outcome)
	require.Equal(t, []byte("first"), mismatch.CurrentTaskToken)
	otherRun := &commonpb.WorkflowExecution{WorkflowId: execution.WorkflowId, RunId: "another-run"}
	require.Equal(t, &workerpb.WorkflowHeartbeatNotificationResult{Outcome: workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH}, r.notify([]byte("first"), otherRun))
	require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify([]byte("first"), execution).GetOutcome())
	<-first
	require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify([]byte("first"), execution).GetOutcome())
	select {
	case <-first:
		t.Fatal("duplicate notification was delivered")
	case <-second:
		t.Fatal("notification reached another task")
	default:
	}
	retireFirst()
	require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify([]byte("first"), execution).GetOutcome())
	replacement, retireReplacement := r.register([]byte("replacement"), execution)
	defer retireReplacement()
	retireFirst()
	require.Equal(t, []byte("replacement"), r.notify([]byte("first"), execution).CurrentTaskToken)
	require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify([]byte("replacement"), execution).GetOutcome())
	<-replacement
	retireReplacement()
	retireReplacement()
	retireSecond()
	require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify([]byte("first"), execution).GetOutcome())
}

func TestWorkflowHeartbeatNotificationRetirementRace(t *testing.T) {
	r := &workflowTaskHeartbeatNotifications{}
	execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
	for range 100 {
		_, retire := r.register([]byte("task"), execution)
		var wg sync.WaitGroup
		wg.Go(func() { r.notify([]byte("task"), execution).GetOutcome() })
		wg.Go(retire)
		wg.Wait()
		require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify([]byte("task"), execution).GetOutcome())
	}
}

func TestWorkerCommandWorkflowHeartbeatNotificationBatch(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	r := &workflowTaskHeartbeatNotifications{}
	execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
	ready, retire := r.register([]byte("active"), execution)
	defer retire()
	cancellations := newActivityCancellationCallbacks()
	activityCtx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	retireActivity := cancellations.register([]byte("activity"), cancel)
	defer retireActivity()
	commands := []*workerpb.WorkerCommand{}
	for _, token := range [][]byte{[]byte("active"), []byte("active"), []byte("missing"), nil} {
		commands = append(commands, &workerpb.WorkerCommand{Type: &workerpb.WorkerCommand_WorkflowHeartbeatNotification{
			WorkflowHeartbeatNotification: &workerpb.WorkflowHeartbeatNotificationCommand{TaskToken: token, Execution: execution},
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
			for i, outcome := range []workerpb.WorkflowHeartbeatNotificationResult_Outcome{
				workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED,
				workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED,
				workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH,
				workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH,
			} {
				require.Equal(t, outcome, response.Results[i].GetWorkflowHeartbeatNotification().GetOutcome())
				if i >= 2 {
					require.Equal(t, []byte("active"), response.Results[i].GetWorkflowHeartbeatNotification().CurrentTaskToken)
				}
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

func TestWorkflowHeartbeatNotificationCommandWorkerExit(t *testing.T) {
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
				worker.started.Store(true)
				processor := &workflowTaskProcessor{basePoller: basePoller{namespaceWorker: worker}}
				processor.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}
				poller := processor.createPoller(NonSticky, nil)
				go worker.run()
				synctest.Wait()
				if status.Code(scenario.heartbeatError) == codes.Unimplemented {
					<-worker.stoppedC
				} else {
					require.True(t, poller.getNextPollRequest().GetCapabilities().GetSupportsWorkflowHeartbeatNotification())
				}
				worker.stop()
				require.False(t, poller.getNextPollRequest().GetCapabilities().GetSupportsWorkflowHeartbeatNotification())
			})
		})
	}
}

func TestWorkflowHeartbeatNotificationAvailability(t *testing.T) {
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
					require.Equal(t, available, request.GetCapabilities().GetSupportsWorkflowHeartbeatNotification())
				}
				client.eagerDispatcher.registerWorker(worker.workflowWorker)
				request := &workflowservice.StartWorkflowExecutionRequest{TaskQueue: &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue}}
				executor := client.eagerDispatcher.applyToRequest(request)
				require.NotNil(t, executor)
				executor.releaseUnused()
				require.Equal(t, available, request.GetEagerWorkerCapabilities().GetSupportsWorkflowHeartbeatNotification())
				if available {
					require.Equal(t, worker.executionParams.namespaceWorker.controlTaskQueue(), request.EagerWorkerControlTaskQueue)
				} else {
					require.Empty(t, request.EagerWorkerControlTaskQueue)
				}
				worker.unregisterHeartbeatWorker()
			})
		})
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowHeartbeatNotificationQueries() {
	for _, standalone := range []bool{true, false} {
		s.Run(map[bool]string{true: "standalone", false: "on normal WFT"}[standalone], func() {
			r := &workflowTaskHeartbeatNotifications{}
			execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
			token := []byte("query-task")
			var outcome workerpb.WorkflowHeartbeatNotificationResult_Outcome
			s.registry.RegisterWorkflowWithOptions(func(ctx Context) error {
				if err := SetQueryHandler(ctx, "status", func() (string, error) {
					outcome = r.notify(token, execution).GetOutcome()
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
			task.task.WorkflowExecution = execution
			query := &querypb.WorkflowQuery{QueryType: "status"}
			if standalone {
				task.task.Query = query
				task.task.PreviousStartedEventId = 3
				task.task.History.Events = append(task.task.History.Events,
					createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2, StartedEventId: 3}))
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
				s.Equal(workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, outcome)
				s.Equal(enumspb.QUERY_RESULT_TYPE_ANSWERED, response.rawRequest.(*workflowservice.RespondQueryTaskCompletedRequest).CompletedType)
			} else {
				s.Equal(workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, outcome)
				s.Equal(enumspb.QUERY_RESULT_TYPE_ANSWERED, response.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest).QueryResults["query"].ResultType)
			}
			s.Equal(workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify(token, execution).GetOutcome())
		})
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowHeartbeatNotificationSafeYield() {
	for _, scenario := range []struct {
		name           string
		localActivity  bool
		readyResult    bool
		heartbeatError error
		timedHeartbeat bool
		stopWorker     bool
		panicWorkflow  bool
	}{
		{name: "normal completion"},
		{name: "pending local activity", localActivity: true},
		{name: "WHN before ready local activity result", localActivity: true, readyResult: true},
		{name: "heartbeat failure", localActivity: true, heartbeatError: errors.New("heartbeat failed")},
		{name: "timed fallback", localActivity: true, timedHeartbeat: true},
		{name: "worker shutdown", localActivity: true, stopWorker: true},
		{name: "workflow panic", panicWorkflow: true},
	} {
		s.Run(scenario.name, func() {
			synctest.Test(s.T(), func(t *testing.T) {
				r := &workflowTaskHeartbeatNotifications{}
				execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
				started, yield := make(chan struct{}), make(chan struct{})
				s.registry.RegisterWorkflowWithOptions(func(ctx Context) error {
					if scenario.readyResult {
						ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})
						future := ExecuteLocalActivity(ctx, func() error { return nil })
						Go(ctx, func(Context) {
							close(started)
							<-yield
						})
						return future.Get(ctx, nil)
					}
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
				}, 0, "WHNSafeYield"), laResultCh: make(chan *localActivityResult)}
				task.task.TaskToken = []byte("executing")
				task.task.WorkflowExecution = execution
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
				if scenario.readyResult {
					for _, activity := range wfctx.getEventHandler().pendingLaTasks {
						go func() {
							select {
							case task.laResultCh <- &localActivityResult{task: activity}:
							case <-done:
							}
						}()
					}
				}
				if !scenario.timedHeartbeat {
					require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(task.task.TaskToken, execution).GetOutcome())
					require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(task.task.TaskToken, execution).GetOutcome())
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
				require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify(task.task.TaskToken, execution).GetOutcome())
			})
		})
	}
}

func (s *TaskHandlersTestSuite) TestWorkflowHeartbeatNotificationReplacement() {
	synctest.Test(s.T(), func(t *testing.T) {
		r := &workflowTaskHeartbeatNotifications{}
		execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
		firstToken, nextToken := []byte("first"), []byte("next")
		var signalSeen, secondSignalSeen bool
		s.registry.RegisterWorkflowWithOptions(func(ctx Context) error {
			if err := SetQueryHandler(ctx, "state", func() ([]bool, error) {
				return []bool{signalSeen, secondSignalSeen}, nil
			}); err != nil {
				return err
			}
			ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})
			activity := ExecuteLocalActivity(ctx, func() error { return nil })
			require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(firstToken, execution).GetOutcome())
			GetSignalChannel(ctx, "interrupt").Receive(ctx, nil)
			signalSeen = true
			NewTimer(ctx, time.Hour)
			require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify(firstToken, execution).GetOutcome())
			GetSignalChannel(ctx, "second").Receive(ctx, nil)
			secondSignalSeen = true
			NewTimer(ctx, 2*time.Hour)
			return activity.Get(ctx, nil)
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
		}, 0, "WHNReplacement"), laResultCh: make(chan *localActivityResult, 1)}
		first.task.TaskToken = firstToken
		first.task.WorkflowExecution = execution
		first.task.StartedEventId = 3
		next := &workflowTask{task: createWorkflowTaskWithQueries([]*historypb.HistoryEvent{
			createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2, StartedEventId: 3}),
			createTestEventWorkflowExecutionSignaled(5, "interrupt"),
			createTestEventWorkflowExecutionSignaled(6, "second"),
			createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{}),
			createTestEventWorkflowTaskStarted(8),
		}, 3, "WHNReplacement", map[string]*querypb.WorkflowQuery{"state": {QueryType: "state"}}, false), laResultCh: first.laResultCh}
		next.task.WorkflowExecution = first.task.WorkflowExecution
		next.task.TaskToken = nextToken
		next.task.StartedEventId = 8
		wfctx, err := handler.GetOrCreateWorkflowContext(first.task, nil)
		require.NoError(t, err)
		defer func() { wfctx.Unlock(err) }()
		heartbeats := 0
		remainingEvents := next.task.History.Events[2:]
		next.task.History.Events = next.task.History.Events[:2]
		next.historyIterator = MockHistoryIterator{
			HasNextPageImpl: func() bool { return len(remainingEvents) > 0 },
			GetNextPageImpl: func() (*historypb.History, error) {
				// Queue WHN while the replacement task still has unread history.
				require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(nextToken, execution).GetOutcome())
				require.Equal(t, 1, heartbeats)
				events := remainingEvents
				remainingEvents = nil
				return &historypb.History{Events: events}, nil
			},
		}
		result, err := handler.ProcessWorkflowTask(first, wfctx, func(completion *workflowTaskCompletion, start time.Time) (*workflowTask, error) {
			heartbeats++
			require.Less(t, time.Since(start), 8*time.Second)
			require.True(t, completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest).ForceCreateNewWorkflowTask)
			if heartbeats == 1 {
				require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify(firstToken, execution).GetOutcome())
				return next, nil
			}
			require.True(t, signalSeen)
			require.True(t, secondSignalSeen)
			require.Equal(t, int64(8), wfctx.lastHandledEventID)
			request := completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest)
			var state []bool
			require.Equal(t, enumspb.QUERY_RESULT_TYPE_ANSWERED, request.QueryResults["state"].ResultType)
			require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(request.QueryResults["state"].Answer, &state))
			require.Equal(t, []bool{true, true}, state)
			events := []*historypb.HistoryEvent{createTestEventWorkflowTaskCompleted(9, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 7, StartedEventId: 8})}
			timers := 0
			for _, command := range request.Commands {
				if command.GetCommandType() == enumspb.COMMAND_TYPE_START_TIMER {
					event := createTestEventTimerStarted(int64(10+timers), 0)
					event.GetTimerStartedEventAttributes().TimerId = command.GetStartTimerCommandAttributes().TimerId
					events = append(events, event)
					timers++
				}
			}
			require.Equal(t, 2, timers)
			events = append(events, createTestEventWorkflowTaskScheduled(12, &historypb.WorkflowTaskScheduledEventAttributes{}), createTestEventWorkflowTaskStarted(13))
			third := &workflowTask{task: createWorkflowTaskWithQueries(events, 8, "WHNReplacement", nil, false), laResultCh: first.laResultCh}
			third.task.WorkflowExecution = execution
			third.task.TaskToken, third.task.StartedEventId = []byte("third"), 13
			for _, activity := range wfctx.getEventHandler().pendingLaTasks {
				first.laResultCh <- &localActivityResult{task: activity}
			}
			return third, nil
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		request := result.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest)
		require.False(t, request.ForceCreateNewWorkflowTask)
		require.Empty(t, request.QueryResults)
		require.Len(t, request.Commands, 2)
		require.Equal(t, enumspb.COMMAND_TYPE_RECORD_MARKER, request.Commands[0].CommandType)
		require.Equal(t, enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION, request.Commands[1].CommandType)
		require.True(t, signalSeen)
		require.Equal(t, 2, heartbeats)
		require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, r.notify(nextToken, execution).GetOutcome())
	})
}

func workflowHeartbeatNotificationReplayWorkflow(ctx Context) (string, error) {
	ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})
	NewTimer(ctx, time.Hour)
	long := ExecuteLocalActivity(ctx, "whn-long")
	short := ExecuteLocalActivity(ctx, "whn-short")
	GetSignalChannel(ctx, "interrupt").Receive(ctx, nil)
	NewTimer(ctx, 2*time.Hour)
	var shortResult, longResult string
	if err := short.Get(ctx, &shortResult); err != nil {
		return "", err
	}
	if err := long.Get(ctx, &longResult); err != nil {
		return "", err
	}
	return shortResult + "/" + longResult, nil
}

func (s *TaskHandlersTestSuite) TestWorkflowHeartbeatNotificationCompletionReplay() {
	synctest.Test(s.T(), func(t *testing.T) {
		const workflowName = "WHNCompletionReplay"
		r := &workflowTaskHeartbeatNotifications{}
		execution := &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}
		firstToken, nextToken := []byte("first"), []byte("next")
		s.registry.RegisterWorkflowWithOptions(func(ctx Context) (string, error) {
			require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(firstToken, execution).Outcome)
			return workflowHeartbeatNotificationReplayWorkflow(ctx)
		}, RegisterWorkflowOptions{Name: workflowName, DisableAlreadyRegisteredCheck: true})
		for _, name := range []string{"whn-long", "whn-short"} {
			s.registry.RegisterActivityWithOptions(func() (string, error) { return "unused", nil }, RegisterActivityOptions{Name: name, DisableAlreadyRegisteredCheck: true})
		}
		params := s.getTestWorkerExecutionParams()
		var cacheLock sync.Mutex
		var lease *workerCacheLease
		params.cache, lease = newWorkerCache(&sharedWorkerCache{}, &cacheLock, defaultStickyCacheSize)
		t.Cleanup(lease.release)
		params.workflowTaskHeartbeatNotifications = r
		handler := newWorkflowTaskHandler(params, nil, s.registry).(*workflowTaskHandlerImpl)
		handler.laTunnel = newLocalActivityTunnel(make(chan struct{}))
		results := make(chan *localActivityResult, 2)
		first := &workflowTask{task: createWorkflowTask([]*historypb.HistoryEvent{
			createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
				WorkflowType:        &commonpb.WorkflowType{Name: workflowName},
				WorkflowTaskTimeout: durationpb.New(10 * time.Second),
				TaskQueue:           &taskqueuepb.TaskQueue{Name: testWorkflowTaskTaskqueue},
			}),
		}, 0, workflowName), laResultCh: results}
		first.task.WorkflowExecution = execution
		first.task.TaskToken, first.task.StartedEventId = firstToken, 3
		wfctx, err := handler.GetOrCreateWorkflowContext(first.task, nil)
		require.NoError(t, err)
		defer func() { wfctx.Unlock(err) }()
		history := &historypb.History{Events: append([]*historypb.HistoryEvent(nil), first.task.History.Events...)}
		var longTask, shortTask *localActivityTask
		heartbeats := 0
		completion, err := handler.ProcessWorkflowTask(first, wfctx, func(completion *workflowTaskCompletion, start time.Time) (*workflowTask, error) {
			heartbeats++
			require.Less(t, time.Since(start), 8*time.Second)
			request := completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest)
			require.True(t, request.ForceCreateNewWorkflowTask)
			// Both heartbeats precede LA result application, so each contains only its timer.
			require.Len(t, request.Commands, 1)
			timer := request.Commands[0].GetStartTimerCommandAttributes()
			require.NotNil(t, timer)
			completedID := int64(len(history.Events) + 1)
			history.Events = append(history.Events, createTestEventWorkflowTaskCompleted(completedID, &historypb.WorkflowTaskCompletedEventAttributes{
				ScheduledEventId: completedID - 2, StartedEventId: completedID - 1, SdkMetadata: request.SdkMetadata,
			}))
			started := createTestEventTimerStarted(completedID+1, 0)
			started.GetTimerStartedEventAttributes().TimerId = timer.TimerId
			started.GetTimerStartedEventAttributes().StartToFireTimeout = timer.StartToFireTimeout
			started.GetTimerStartedEventAttributes().WorkflowTaskCompletedEventId = completedID
			events := []*historypb.HistoryEvent{history.Events[len(history.Events)-1], started}
			if heartbeats == 1 {
				for _, activity := range wfctx.getEventHandler().pendingLaTasks {
					if activity.params.ActivityType == "whn-short" {
						shortTask = activity
					} else {
						longTask = activity
					}
				}
				require.NotNil(t, shortTask)
				require.NotNil(t, longTask)
				events = append(events, createTestEventWorkflowExecutionSignaled(6, "interrupt"))
			}
			scheduledID := completedID + int64(len(events))
			events = append(events, createTestEventWorkflowTaskScheduled(scheduledID, &historypb.WorkflowTaskScheduledEventAttributes{}), createTestEventWorkflowTaskStarted(scheduledID+1))
			history.Events = append(history.Events, events[1:]...)
			next := &workflowTask{task: createWorkflowTaskWithQueries(events, completedID-1, workflowName, nil, false), laResultCh: results}
			next.task.WorkflowExecution = execution
			next.task.StartedEventId = scheduledID + 1
			if heartbeats == 1 {
				next.task.TaskToken = nextToken
				remaining := events[2:]
				next.task.History.Events = events[:2]
				next.historyIterator = MockHistoryIterator{
					HasNextPageImpl: func() bool { return len(remaining) > 0 },
					GetNextPageImpl: func() (*historypb.History, error) {
						require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(nextToken, execution).Outcome)
						payloads, encodeErr := converter.GetDefaultDataConverter().ToPayloads("short")
						require.NoError(t, encodeErr)
						results <- &localActivityResult{task: shortTask, result: payloads}
						page := &historypb.History{Events: remaining}
						remaining = nil
						return page, nil
					},
				}
			} else {
				require.Equal(t, 2, heartbeats)
				require.Len(t, wfctx.getEventHandler().pendingLaTasks, 2)
				next.task.TaskToken = []byte("third")
				payloads, encodeErr := converter.GetDefaultDataConverter().ToPayloads("long")
				require.NoError(t, encodeErr)
				results <- &localActivityResult{task: longTask, result: payloads}
			}
			return next, nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, heartbeats)
		request := completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest)
		require.False(t, request.ForceCreateNewWorkflowTask)
		require.Equal(t, []byte("third"), request.TaskToken)
		require.Len(t, request.Commands, 3)
		history.Events = append(history.Events, createTestEventWorkflowTaskCompleted(13, &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 11, StartedEventId: 12, SdkMetadata: request.SdkMetadata}))
		for i, activity := range []*localActivityTask{shortTask, longTask} {
			marker := request.Commands[i].GetRecordMarkerCommandAttributes()
			require.NotNil(t, marker)
			var data localActivityMarkerData
			require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(marker.Details[localActivityMarkerDataName], &data))
			require.Equal(t, activity.activityID, data.ActivityID)
			history.Events = append(history.Events, createTestEventMarkerRecorded(int64(14+i), &historypb.MarkerRecordedEventAttributes{MarkerName: marker.MarkerName, Details: marker.Details, WorkflowTaskCompletedEventId: 13}))
		}
		result := request.Commands[2].GetCompleteWorkflowExecutionCommandAttributes().Result
		var value string
		require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(result, &value))
		require.Equal(t, "short/long", value)
		history.Events = append(history.Events, createTestEventWorkflowExecutionCompleted(16, &historypb.WorkflowExecutionCompletedEventAttributes{WorkflowTaskCompletedEventId: 13, Result: result}))
		replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
		require.NoError(t, err)
		replayer.RegisterWorkflowWithOptions(workflowHeartbeatNotificationReplayWorkflow, RegisterWorkflowOptions{Name: workflowName})
		require.NoError(t, replayer.ReplayWorkflowHistory(ilog.NewNopLogger(), history))
	})
}

func (s *TaskHandlersTestSuite) TestWorkflowHeartbeatNotificationColdReplay() {
	synctest.Test(s.T(), func(t *testing.T) {
		history, err := extractHistoryFromFile("../test/replaytests/workflow-heartbeat-notification.json", 12)
		require.NoError(t, err)
		r := &workflowTaskHeartbeatNotifications{}
		s.registry.RegisterWorkflowWithOptions(workflowHeartbeatNotificationReplayWorkflow, RegisterWorkflowOptions{Name: "WHNCompletionReplay", DisableAlreadyRegisteredCheck: true})
		for _, name := range []string{"whn-long", "whn-short"} {
			s.registry.RegisterActivityWithOptions(func() (string, error) { return "unused", nil }, RegisterActivityOptions{Name: name, DisableAlreadyRegisteredCheck: true})
		}
		params := s.getTestWorkerExecutionParams()
		var cacheLock sync.Mutex
		var lease *workerCacheLease
		params.cache, lease = newWorkerCache(&sharedWorkerCache{}, &cacheLock, defaultStickyCacheSize)
		t.Cleanup(lease.release)
		params.workflowTaskHeartbeatNotifications = r
		handler := newWorkflowTaskHandler(params, nil, s.registry).(*workflowTaskHandlerImpl)
		handler.laTunnel = newLocalActivityTunnel(make(chan struct{}))
		// A fresh context must replay two completed WFTs before reaching this live WFT.
		task := &workflowTask{task: createWorkflowTaskWithQueries(history.Events[:6], 8, "WHNCompletionReplay", nil, false)}
		task.task.TaskToken, task.task.StartedEventId = []byte("cold"), 12
		heartbeats := 0
		remaining := history.Events[6:]
		task.historyIterator = MockHistoryIterator{
			HasNextPageImpl: func() bool { return len(remaining) > 0 },
			GetNextPageImpl: func() (*historypb.History, error) {
				require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, r.notify(task.task.TaskToken, task.task.WorkflowExecution).Outcome)
				require.Zero(t, heartbeats)
				require.Nil(t, handler.laTunnel.taskQueueHead)
				page := &historypb.History{Events: remaining}
				remaining = nil
				return page, nil
			},
		}
		wfctx, err := handler.GetOrCreateWorkflowContext(task.task, task.historyIterator)
		require.NoError(t, err)
		defer func() { wfctx.Unlock(err) }()
		result, err := handler.ProcessWorkflowTask(task, wfctx, func(completion *workflowTaskCompletion, start time.Time) (*workflowTask, error) {
			heartbeats++
			require.Less(t, time.Since(start), 8*time.Second)
			require.Empty(t, remaining)
			require.Equal(t, int64(12), wfctx.lastHandledEventID)
			require.False(t, wfctx.getEventHandler().isReplay)
			require.Len(t, wfctx.getEventHandler().pendingLaTasks, 2)
			queued := 0
			for activity := handler.laTunnel.taskQueueHead; activity != nil; activity = activity.nextQueuedTask {
				queued++
			}
			require.Equal(t, 2, queued)
			request := completion.rawRequest.(*workflowservice.RespondWorkflowTaskCompletedRequest)
			require.Equal(t, task.task.TaskToken, request.TaskToken)
			require.True(t, request.ForceCreateNewWorkflowTask)
			// Historical timers have been matched during replay, not emitted a second time.
			require.Empty(t, request.Commands)
			return nil, nil
		})
		require.NoError(t, err)
		require.Nil(t, result)
		require.Equal(t, 1, heartbeats)
	})
}

func TestWorkflowHeartbeatNotificationRPC(t *testing.T) {
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
						require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_ACCEPTED, response.Results[0].GetWorkflowHeartbeatNotification().GetOutcome())
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
					Commands: []*workerpb.WorkerCommand{{Type: &workerpb.WorkerCommand_WorkflowHeartbeatNotification{
						WorkflowHeartbeatNotification: &workerpb.WorkflowHeartbeatNotificationCommand{TaskToken: first.TaskToken, Execution: first.WorkflowExecution},
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
				require.Equal(t, workerpb.WorkflowHeartbeatNotificationResult_OUTCOME_TASK_TOKEN_MISMATCH, shared.workflowTaskHeartbeatNotifications.notify(next.TaskToken, next.WorkflowExecution).GetOutcome())
			})
		})
	}
}
