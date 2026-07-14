package test_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	nexusUpdateTestTimeout = 20 * time.Second
)

var (
	invalidIncrementError = errors.New("invalid increment")
)

type updateAddInput struct {
	UpdateID           string
	WorkflowID         string
	Amount             int
	SleepDuration      time.Duration
	ExpectSyncResponse bool // hint to the caller workflow that the async token will not be set (a retried completed update is sync)
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
	ctx, cancel := context.WithTimeout(context.TODO(), nexusUpdateTestTimeout)
	defer cancel()

	addOp, err := temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[updateAddInput, updateAddOutput]{
		Name: addOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input updateAddInput, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[updateAddOutput], error) {
			return temporalnexus.StartUpdateWorkflow[updateAddOutput](ctx, nc, client.UpdateWorkflowOptions{
				WorkflowID:   input.WorkflowID,
				UpdateID:     input.UpdateID,
				UpdateName:   addUpdate,
				Args:         []any{input.Amount, input.SleepDuration},
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
				Args:         []any{input.Amount, input.SleepDuration},
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
	callerWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                  "delayed-" + uuid.NewString(),
		TaskQueue:           callerTaskQueue,
		WorkflowTaskTimeout: time.Second,
	}, callerWorkflow, updateAddInput{WorkflowID: handlerWorkflowID, Amount: 5})
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

	// verify count, no errors
	var out updateAddOutput
	ts.NoError(callerWorkflowRun.Get(ctx, &out))
	ts.Equal(out.Count, 5)
}

func counterWorkflow(ctx workflow.Context) (int, error) {
	counter := 0

	updateHandler := func(ctx workflow.Context, amount int, sleepDuration time.Duration) (updateAddOutput, error) {
		counter += amount
		newCounterVal := counter
		if sleepDuration != 0 {
			_ = workflow.Sleep(ctx, sleepDuration)
		}
		return updateAddOutput{Count: newCounterVal}, nil
	}

	// used for testing invalid updates
	updateValidator := func(ctx workflow.Context, amount int, sleepDuration time.Duration) error {
		if amount%5 != 0 {
			return invalidIncrementError
		}
		return nil
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx,
		addUpdate,
		updateHandler,
		workflow.UpdateHandlerOptions{
			Validator: updateValidator,
		},
	); err != nil {
		return 0, err
	}

	workflow.GetSignalChannel(ctx, doneSignal).Receive(ctx, nil)
	workflow.GetLogger(ctx).Info("finished workflow, exiting now...", "final counter", counter)
	return counter, nil
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

func checkForLink(links []*common.Link, requiredLink *common.Link) bool {
	for _, link := range links {
		if link.Equal(requiredLink) {
			return true
		}
	}
	return false
}

func getEventLinks(ctx context.Context,
	c client.Client, workflowRun client.WorkflowRun,
	filter func(e *history.HistoryEvent) bool,
) []*common.Link {
	events := getEvents(ctx, c, workflowRun, filter)
	eventLinks := make([]*common.Link, 0, len(events))
	for _, event := range events {
		eventLinks = append(eventLinks, event.GetLinks()...)
	}
	return eventLinks
}

func getEventIDByType(ctx context.Context, c client.Client, workflowRun client.WorkflowRun, eventType enums.EventType) int64 {
	events := getEvents(ctx, c, workflowRun, func(e *history.HistoryEvent) bool {
		return e.GetEventType() == eventType
	})
	if len(events) == 0 {
		return -1
	}
	return events[0].EventId
}

// get the request ID of the singular nexus op in this workflow run
func getNexusOpRequestID(ctx context.Context, c client.Client, workflowRun client.WorkflowRun) (string, error) {
	events := getEvents(ctx, c, workflowRun, func(e *history.HistoryEvent) bool {
		return e.GetEventType() == enums.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED
	})
	if len(events) == 0 {
		return "", errors.New("no nexus op event found")
	}
	if len(events) > 1 {
		return "", errors.New("multiple nexus ops events found, cannot determine specific requestID")
	}
	return events[0].GetNexusOperationScheduledEventAttributes().RequestId, nil
}

func getEvents(ctx context.Context,
	c client.Client, workflowRun client.WorkflowRun,
	filter func(e *history.HistoryEvent) bool,
) []*history.HistoryEvent {
	iter := c.GetWorkflowHistory(ctx,
		workflowRun.GetID(), workflowRun.GetRunID(),
		false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	events := []*history.HistoryEvent{}
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			continue
		}
		if filter(event) {
			events = append(events, event)
		}
	}
	return events
}
