package test_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"
)

const (
	nexusUpdateTestTimeout = 30 * time.Second
)

var (
	invalidIncrementError = errors.New("invalid increment")
)

type updateAddInput struct {
	UpdateID           string
	WorkflowID         string
	Amount             int
	SleepDuration      time.Duration
	WaitForSignal      string
	ExpectSyncResponse bool // hint to the caller workflow that the async token will not be set (a retried completed update is sync)
	LoseFirstResponse  bool // the update-with-start handler fails its first successful attempt so the Nexus task is retried
}

type updateAddOutput struct {
	Count int
}

const (
	serviceName  = "counter-update-service"
	addOperation = "addOperation"
	addUpdate    = "addUpdate"
	doneSignal   = "done"

	taskQueue = "nexusOpUpdateWorkflowTQ"
)

func (ts *IntegrationTestSuite) TestNexusUpdateWorkflowOperation() {
	if os.Getenv("DISABLE_STANDALONE_NEXUS_TESTS") != "" {
		ts.T().SkipNow()
	}
	ctx, cancel := context.WithTimeout(context.TODO(), nexusUpdateTestTimeout)
	defer cancel()

	addOp, err := temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[updateAddInput, updateAddOutput]{
		Name: addOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input updateAddInput, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[updateAddOutput], error) {
			return temporalnexus.StartUpdateWorkflow[updateAddOutput](ctx, nc, client.UpdateWorkflowOptions{
				WorkflowID:   input.WorkflowID,
				UpdateID:     input.UpdateID,
				UpdateName:   addUpdate,
				Args:         []any{input.Amount, input.SleepDuration, input.WaitForSignal},
				WaitForStage: client.WorkflowUpdateStageAccepted,
			})
		},
	})
	ts.NoError(err)

	endpoint := "update-workflow-backed-nexus-ep-" + uuid.NewString()
	_, err = ts.client.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpoint,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: ts.config.Namespace,
						TaskQueue: taskQueue,
					},
				},
			},
		},
	})
	ts.NoError(err)

	callerWorkflow := getCallerWorkflow(addOp, endpoint)

	w := worker.New(ts.client, taskQueue, worker.Options{})
	service := nexus.NewService(serviceName)
	ts.NoError(service.Register(addOp))

	w.RegisterNexusService(service)
	w.RegisterWorkflow(counterWorkflow)
	w.RegisterWorkflow(callerWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()

	defaultSleep := 1 * time.Second

	counterWorkflowID := "counter-" + uuid.NewString()
	counterWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        counterWorkflowID,
		TaskQueue: taskQueue,
	}, counterWorkflow)
	ts.NoError(err)

	stopCounterWorkflow := func() {
		ts.NoError(ts.client.SignalWorkflow(ctx, counterWorkflowID, "", doneSignal, nil))
		ts.NoError(counterWorkflowRun.Get(ctx, nil))
	}

	ts.Run("Verify operation doesnt retry on validation failure", func() {
		invalidUpdateWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "validation-failure-" + uuid.NewString(),
			TaskQueue:           taskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 6, SleepDuration: defaultSleep})
		ts.NoError(err, invalidIncrementError)
		ts.ErrorContains(invalidUpdateWorkflowRun.Get(ctx, nil), invalidIncrementError.Error())
	})

	ts.Run("Verify valid update operation succeeds and has links", func() {
		var out updateAddOutput

		counterUpdateWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "caller-" + uuid.NewString(),
			TaskQueue:           taskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: "BasicSuccessfulUpdate",
			SleepDuration: defaultSleep})
		ts.NoError(err)
		ts.NoError(counterUpdateWorkflowRun.Get(ctx, &out))
		ts.Equal(5, out.Count)

		eventsFilter := func(e *history.HistoryEvent) bool {
			filterOpTypes := []enums.EventType{
				enums.EVENT_TYPE_NEXUS_OPERATION_STARTED,            // for forward events links on caller
				enums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED, // for backward events links on handler
			}
			return slices.Contains(filterOpTypes, e.EventType)
		}
		callerRequestID, err := getNexusOpRequestID(ctx, ts.client, counterUpdateWorkflowRun)
		ts.NoError(err)
		// from caller ns to target ns
		forwardLink := &common.Link{Variant: &common.Link_WorkflowEvent_{WorkflowEvent: &common.Link_WorkflowEvent{
			Namespace:  ts.config.Namespace,
			WorkflowId: counterWorkflowID,
			RunId:      counterWorkflowRun.GetRunID(),
			Reference: &common.Link_WorkflowEvent_RequestIdRef{RequestIdRef: &common.Link_WorkflowEvent_RequestIdReference{
				RequestId: callerRequestID,
				EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED,
			}},
		}}}
		// from target ns back to caller ns
		backwardLink := &common.Link{Variant: &common.Link_WorkflowEvent_{WorkflowEvent: &common.Link_WorkflowEvent{
			Namespace:  ts.config.Namespace,
			WorkflowId: counterUpdateWorkflowRun.GetID(),
			RunId:      counterUpdateWorkflowRun.GetRunID(),
			Reference: &common.Link_WorkflowEvent_EventRef{EventRef: &common.Link_WorkflowEvent_EventReference{
				EventId:   getEventIDByType(ctx, ts.client, counterUpdateWorkflowRun, enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED),
				EventType: enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED,
			}},
		}}}
		counterWorkflowLinks := getEventLinks(ctx, ts.client, counterWorkflowRun, eventsFilter)
		callerWorkflowLinks := getEventLinks(ctx, ts.client, counterUpdateWorkflowRun, eventsFilter)
		ts.True(checkForLink(counterWorkflowLinks, backwardLink))
		ts.True(checkForLink(callerWorkflowLinks, forwardLink))
	})

	ts.Run("Verify sequential updates with same UpdateID are idempotent", func() {
		var out updateAddOutput
		updateID := "consistentIDForRetries"

		counterUpdateWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "caller-" + uuid.NewString(),
			TaskQueue:           taskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: updateID})
		ts.NoError(err)
		ts.NoError(counterUpdateWorkflowRun.Get(ctx, &out))
		ts.Equal(10, out.Count)

		counterUpdateWorkflowRun, err = ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "caller-" + uuid.NewString(),
			TaskQueue:           taskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: updateID, ExpectSyncResponse: true})
		ts.NoError(err)
		ts.NoError(counterUpdateWorkflowRun.Get(ctx, &out))
		ts.Equal(10, out.Count) // shouldnt increment again as its same updateID
	})

	ts.Run("Verify concurrent Updates with same UpdateID are idempotent and finish", func() {
		// simulate multiple updates with same ID hitting at same time
		// the counter sleeps for second so all of them should get
		// an async result- dont set the ExpectSyncResponse for that reason
		gate := make(chan struct{})
		wg := sync.WaitGroup{}
		parallelUpdateID := "consistent-" + uuid.NewString()
		numParallelUpdates := 3
		wg.Add(numParallelUpdates)
		errs := make([]error, numParallelUpdates)
		vals := make([]updateAddOutput, numParallelUpdates)
		for i := range numParallelUpdates {
			go func(i int) {
				defer wg.Done()
				<-gate
				run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
					ID:                  fmt.Sprintf("parallel-caller-%d", i),
					TaskQueue:           taskQueue,
					WorkflowTaskTimeout: time.Second,
				}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: parallelUpdateID,
					SleepDuration: defaultSleep})
				if err != nil {
					errs[i] = err
					return
				}
				errs[i] = run.Get(ctx, &vals[i])
			}(i)
		}
		close(gate)
		wg.Wait()
		for i := range numParallelUpdates {
			ts.NoError(errs[i])
			ts.Equal(15, vals[i].Count)
		}
	})

	ts.Run("Verify immediate returns are still async", func() {
		// See NEXUS-489 for details.
		var out updateAddOutput
		counterUpdateWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        "immediate-return" + uuid.NewString(),
			TaskQueue: taskQueue,
		}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, SleepDuration: 0})
		ts.NoError(err)
		ts.NoError(counterUpdateWorkflowRun.Get(ctx, &out))
		ts.Equal(20, out.Count)
	})

	ts.Run("Verify updates on completed workflows fail", func() {
		stopCounterWorkflow()
		// updates on finished workflows should fail
		counterUpdateWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "failing-caller-" + uuid.NewString(),
			TaskQueue:           taskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, updateAddInput{WorkflowID: counterWorkflowID, Amount: 5})
		ts.NoError(err)
		ts.Error(counterUpdateWorkflowRun.Get(ctx, nil))
	})
}

// separate from [IntegrationTestSuite.TestNexusUpdateWorkflowOperation] as this is a more complicated scenario
func (ts *IntegrationTestSuite) TestNexusUpdateWorkflowDelayedOperation() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "Nexus update tests create namespace endpoints through Operator Service")
	if os.Getenv("DISABLE_STANDALONE_NEXUS_TESTS") != "" {
		ts.T().SkipNow()
	}
	ctx, cancel := context.WithTimeout(context.TODO(), nexusUpdateTestTimeout)
	defer cancel()

	handlerTaskQueue, callerTaskQueue := "counterTQ", "updaterTQ"

	addOp, err := temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[updateAddInput, updateAddOutput]{
		Name: addOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input updateAddInput, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[updateAddOutput], error) {
			return temporalnexus.StartUpdateWorkflow[updateAddOutput](ctx, nc, client.UpdateWorkflowOptions{
				WorkflowID:   input.WorkflowID,
				UpdateID:     input.UpdateID,
				UpdateName:   addUpdate,
				Args:         []any{input.Amount, input.SleepDuration, input.WaitForSignal},
				WaitForStage: client.WorkflowUpdateStageAccepted,
			})
		},
	})
	ts.NoError(err)

	endpoint := "update-workflow-backed-nexus-ep-" + uuid.NewString()
	_, err = ts.client.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpoint,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: ts.config.Namespace,
						TaskQueue: handlerTaskQueue,
					},
				},
			},
		},
	})
	ts.NoError(err)

	callerWorkflow := getCallerWorkflow(addOp, endpoint)

	callerWorker := worker.New(ts.client, callerTaskQueue, worker.Options{})

	callerWorker.RegisterWorkflow(callerWorkflow)
	ts.NoError(callerWorker.Start())
	defer callerWorker.Stop()

	// run the counter workflow
	handlerWorkflowID := "counter-" + uuid.NewString()
	handlerWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        handlerWorkflowID,
		TaskQueue: handlerTaskQueue,
	}, counterWorkflow)
	ts.NoError(err)

	stopCounterWf := func() {
		ts.NoError(ts.client.SignalWorkflow(ctx, handlerWorkflowID, "", doneSignal, nil))
		ts.NoError(handlerWorkflowRun.Get(ctx, nil))
	}

	// start the update, it will get admitted
	releaseSignal := "release-" + uuid.NewString()
	callerWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                  "delayed-" + uuid.NewString(),
		TaskQueue:           callerTaskQueue,
		WorkflowTaskTimeout: time.Second,
	}, callerWorkflow, updateAddInput{
		WorkflowID:    handlerWorkflowID,
		Amount:        5,
		WaitForSignal: releaseSignal,
	})
	ts.NoError(err)

	// now, start the worker so that counter workflow can actually handle the update
	handlerWorker := worker.New(ts.client, handlerTaskQueue, worker.Options{})
	handlerWorker.RegisterWorkflow(counterWorkflow)
	service := nexus.NewService(serviceName)
	ts.NoError(service.Register(addOp))
	handlerWorker.RegisterNexusService(service)
	ts.NoError(handlerWorker.Start())
	defer handlerWorker.Stop()
	defer stopCounterWf()

	ts.waitForHistoryEvent(
		callerWorkflowRun.GetID(),
		callerWorkflowRun.GetRunID(),
		enums.EVENT_TYPE_NEXUS_OPERATION_STARTED,
		10*time.Second,
	)
	ts.NoError(ts.client.SignalWorkflow(ctx, handlerWorkflowID, "", releaseSignal, nil))

	// verify count, no errors
	var out updateAddOutput
	ts.NoError(callerWorkflowRun.Get(ctx, &out))
	ts.Equal(out.Count, 5)
}

func (ts *IntegrationTestSuite) TestNexusUpdateWithStartWorkflowOperation() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "Nexus update tests create namespace endpoints through Operator Service")
	if os.Getenv("DISABLE_STANDALONE_NEXUS_TESTS") != "" {
		ts.T().SkipNow()
	}
	ctx, cancel := context.WithTimeout(context.TODO(), nexusUpdateTestTimeout)
	defer cancel()

	uwsTaskQueue := "nexusOpUpdateWithStartTQ-" + uuid.NewString()
	var lostResponse atomic.Bool
	addOp, err := temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[updateAddInput, updateAddOutput]{
		Name: addOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input updateAddInput, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[updateAddOutput], error) {
			// TaskQueue is omitted to exercise defaulting to the handler's task queue
			startOp := nc.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
				ID:                       input.WorkflowID,
				WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
			}, counterWorkflow)
			res, err := temporalnexus.StartUpdateWithStartWorkflow[updateAddOutput](ctx, nc, client.UpdateWithStartWorkflowOptions{
				StartWorkflowOperation: startOp,
				// WorkflowID is left empty to exercise defaulting to the start's workflow ID
				UpdateOptions: client.UpdateWorkflowOptions{
					UpdateID:     input.UpdateID,
					UpdateName:   addUpdate,
					Args:         []any{input.Amount, input.SleepDuration, input.WaitForSignal},
					WaitForStage: client.WorkflowUpdateStageAccepted,
				},
			})
			if err == nil && input.LoseFirstResponse && lostResponse.CompareAndSwap(false, true) {
				// the update-with-start went through, but the response never reaches the caller
				return temporalnexus.TemporalOperationResult[updateAddOutput]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "simulated lost response")
			}
			return res, err
		},
	})
	ts.Require().NoError(err)

	endpoint := "update-with-start-nexus-ep-" + uuid.NewString()
	_, err = ts.client.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpoint,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: ts.config.Namespace,
						TaskQueue: uwsTaskQueue,
					},
				},
			},
		},
	})
	ts.Require().NoError(err)

	callerWorkflow := getCallerWorkflow(addOp, endpoint)

	// start and update request IDs of every update-with-start sent by the handler worker, by workflow ID
	var requestIDsMu sync.Mutex
	startRequestIDs := map[string][]string{}
	updateRequestIDs := map[string][]string{}
	startRequestIDsFor := func(workflowID string) []string {
		requestIDsMu.Lock()
		defer requestIDsMu.Unlock()
		return slices.Clone(startRequestIDs[workflowID])
	}
	updateRequestIDsFor := func(workflowID string) []string {
		requestIDsMu.Lock()
		defer requestIDsMu.Unlock()
		return slices.Clone(updateRequestIDs[workflowID])
	}
	handlerClient, err := ts.newIntegrationTestClient(func(options *client.Options) {
		options.ConnectionOptions.DialOptions = append(options.ConnectionOptions.DialOptions,
			grpc.WithChainUnaryInterceptor(func(
				ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
				invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
			) error {
				if r, ok := req.(*workflowservice.ExecuteMultiOperationRequest); ok {
					start := r.GetOperations()[0].GetStartWorkflow()
					requestIDsMu.Lock()
					startRequestIDs[start.GetWorkflowId()] = append(startRequestIDs[start.GetWorkflowId()], start.GetRequestId())
					update := r.GetOperations()[1].GetUpdateWorkflow().GetRequest()
					updateRequestIDs[start.GetWorkflowId()] = append(updateRequestIDs[start.GetWorkflowId()], update.GetRequestId())
					requestIDsMu.Unlock()
				}
				return invoker(ctx, method, req, reply, cc, opts...)
			}))
	})
	ts.Require().NoError(err)
	defer handlerClient.Close()

	w := worker.New(handlerClient, uwsTaskQueue, worker.Options{})
	service := nexus.NewService(serviceName)
	ts.Require().NoError(service.Register(addOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(counterWorkflow)
	w.RegisterWorkflow(callerWorkflow)
	ts.Require().NoError(w.Start())
	defer w.Stop()

	startCaller := func(input updateAddInput) client.WorkflowRun {
		run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "uws-caller-" + uuid.NewString(),
			TaskQueue:           uwsTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, input)
		ts.Require().NoError(err)
		return run
	}
	stopCounter := func(workflowID string) {
		ts.NoError(ts.client.SignalWorkflow(ctx, workflowID, "", doneSignal, nil))
		ts.NoError(ts.client.GetWorkflow(ctx, workflowID, "").Get(ctx, nil))
	}
	eventTypeFilter := func(t enums.EventType) func(*history.HistoryEvent) bool {
		return func(e *history.HistoryEvent) bool { return e.EventType == t }
	}

	ts.Run("starts missing workflow", func() {
		counterWorkflowID := "counter-" + uuid.NewString()
		updateID := "uws-" + uuid.NewString()
		callerRun := startCaller(updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: updateID, SleepDuration: time.Second})
		defer stopCounter(counterWorkflowID)
		var out updateAddOutput
		ts.NoError(callerRun.Get(ctx, &out))
		ts.Equal(5, out.Count)

		counterRun := ts.client.GetWorkflow(ctx, counterWorkflowID, "")
		callerRequestID, err := getNexusOpRequestID(ctx, ts.client, callerRun)
		ts.NoError(err)
		// the start gets a stable request ID derived from the operation's, so a retried Nexus task is
		// deduplicated without sharing the update's request ID
		startIDs := startRequestIDsFor(counterWorkflowID)
		ts.Require().NotEmpty(startIDs, "handler sent no ExecuteMultiOperation request")
		ts.Equal(uuid.NewSHA1(uuid.Nil, []byte(callerRequestID+":start")).String(), startIDs[0])
		// the update carries the operation's request ID, as in the standalone update operation
		ts.Equal(callerRequestID, updateRequestIDsFor(counterWorkflowID)[0])

		// the update's link names the operation's request ID and the Accepted event it records
		forwardLink := &common.Link{Variant: &common.Link_WorkflowEvent_{WorkflowEvent: &common.Link_WorkflowEvent{
			Namespace:  ts.config.Namespace,
			WorkflowId: counterWorkflowID,
			RunId:      counterRun.GetRunID(),
			Reference: &common.Link_WorkflowEvent_RequestIdRef{RequestIdRef: &common.Link_WorkflowEvent_RequestIdReference{
				RequestId: callerRequestID,
				EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED,
			}},
		}}}
		backwardLink := &common.Link{Variant: &common.Link_WorkflowEvent_{WorkflowEvent: &common.Link_WorkflowEvent{
			Namespace:  ts.config.Namespace,
			WorkflowId: callerRun.GetID(),
			RunId:      callerRun.GetRunID(),
			Reference: &common.Link_WorkflowEvent_EventRef{EventRef: &common.Link_WorkflowEvent_EventReference{
				EventId:   getEventIDByType(ctx, ts.client, callerRun, enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED),
				EventType: enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED,
			}},
		}}}
		// start links prove the start request carried the Nexus links, update links prove the update did
		ts.True(checkForLink(getEventLinks(ctx, ts.client, counterRun, eventTypeFilter(enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED)), backwardLink))
		ts.True(checkForLink(getEventLinks(ctx, ts.client, counterRun, eventTypeFilter(enums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED)), backwardLink))
		ts.True(checkForLink(getEventLinks(ctx, ts.client, callerRun, eventTypeFilter(enums.EVENT_TYPE_NEXUS_OPERATION_STARTED)), forwardLink))

		// the async token names the run the server started, which the handler could not know up front
		started := getEvents(ctx, ts.client, callerRun, eventTypeFilter(enums.EVENT_TYPE_NEXUS_OPERATION_STARTED))
		ts.Require().Len(started, 1)
		tokenJSON, err := base64.RawURLEncoding.DecodeString(started[0].GetNexusOperationStartedEventAttributes().GetOperationToken())
		ts.NoError(err)
		var token struct {
			WorkflowID string `json:"wid"`
			RunID      string `json:"rid"`
			UpdateID   string `json:"uid"`
		}
		ts.NoError(json.Unmarshal(tokenJSON, &token))
		ts.Equal(counterWorkflowID, token.WorkflowID)
		ts.Equal(counterRun.GetRunID(), token.RunID)
		ts.Equal(updateID, token.UpdateID)
	})

	ts.Run("immediate then repeated update", func() {
		counterWorkflowID := "counter-" + uuid.NewString()
		updateID := "uws-" + uuid.NewString()
		// an update that completes right away is still async
		callerRun := startCaller(updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: updateID})
		defer stopCounter(counterWorkflowID)
		var out updateAddOutput
		ts.NoError(callerRun.Get(ctx, &out))
		ts.Equal(5, out.Count)

		// the same UpdateID is deduplicated and returns the completed result synchronously
		callerRun = startCaller(updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: updateID, ExpectSyncResponse: true})
		ts.NoError(callerRun.Get(ctx, &out))
		ts.Equal(5, out.Count)
	})

	ts.Run("rejected update fails operation", func() {
		counterWorkflowID := "counter-" + uuid.NewString()
		callerRun := startCaller(updateAddInput{WorkflowID: counterWorkflowID, Amount: 3, UpdateID: "uws-" + uuid.NewString()})
		defer stopCounter(counterWorkflowID)
		ts.ErrorContains(callerRun.Get(ctx, nil), invalidIncrementError.Error())
		// the rejection fails the operation synchronously, so it never started
		ts.Empty(getEvents(ctx, ts.client, callerRun, eventTypeFilter(enums.EVENT_TYPE_NEXUS_OPERATION_STARTED)))
	})

	ts.Run("uses existing workflow", func() {
		counterWorkflowID := "counter-" + uuid.NewString()
		existingRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        counterWorkflowID,
			TaskQueue: uwsTaskQueue,
		}, counterWorkflow)
		ts.Require().NoError(err)
		defer stopCounter(counterWorkflowID)

		callerRun := startCaller(updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, UpdateID: "uws-" + uuid.NewString(), SleepDuration: time.Second})
		var out updateAddOutput
		ts.NoError(callerRun.Get(ctx, &out))
		ts.Equal(5, out.Count)
		// USE_EXISTING applied the update to the running workflow instead of starting a new run
		ts.Len(getEvents(ctx, ts.client, existingRun, eventTypeFilter(enums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED)), 1)
	})

	ts.Run("retry after lost response", func() {
		counterWorkflowID := "counter-" + uuid.NewString()
		releaseSignal := "release-" + uuid.NewString()
		// UpdateID is left empty so it defaults to the request ID, which is stable across retries.
		// The update waits for a signal so it is still pending when the retried task runs; a completed
		// update would complete the operation through its callback before the retry.
		callerRun := startCaller(updateAddInput{WorkflowID: counterWorkflowID, Amount: 5, WaitForSignal: releaseSignal, LoseFirstResponse: true})
		defer stopCounter(counterWorkflowID)
		ts.waitForHistoryEvent(callerRun.GetID(), callerRun.GetRunID(), enums.EVENT_TYPE_NEXUS_OPERATION_STARTED, 10*time.Second)
		ts.NoError(ts.client.SignalWorkflow(ctx, counterWorkflowID, "", releaseSignal, nil))
		ts.NoError(callerRun.Get(ctx, nil))
		ts.GreaterOrEqual(len(startRequestIDsFor(counterWorkflowID)), 2, "the Nexus task was not retried")
		// the caller can't see a duplicate update, so check history
		counterRun := ts.client.GetWorkflow(ctx, counterWorkflowID, "")
		ts.Len(getEvents(ctx, ts.client, counterRun, eventTypeFilter(enums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED)), 1)
	})
}

// caller workflow
func getCallerWorkflow(
	addOp nexus.Operation[updateAddInput, updateAddOutput],
	endpoint string,
) func(workflow.Context, updateAddInput) (updateAddOutput, error) {
	return func(ctx workflow.Context, input updateAddInput) (updateAddOutput, error) {
		nc := workflow.NewNexusClient(endpoint, serviceName)
		fut := nc.ExecuteOperation(ctx, addOp, input, workflow.NexusOperationOptions{
			Summary: "TODO",
		})

		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return updateAddOutput{}, err
		}

		if input.ExpectSyncResponse {
			if exec.OperationToken != "" {
				return updateAddOutput{}, errors.New("unexpected operation token on a sync operation")
			}
		} else {
			if exec.OperationToken == "" {
				return updateAddOutput{}, errors.New("expected a non-empty async operation token")
			}
		}

		var out updateAddOutput
		if err := fut.Get(ctx, &out); err != nil {
			return updateAddOutput{}, err
		}

		return out, nil
	}
}
