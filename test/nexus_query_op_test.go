package test_test

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	nexusQueryTestTimeout = 30 * time.Second
)

const (
	queryService    = "query-service"
	queryOperation  = "queryOperation"
	queryOp         = "queryOp"
	queryDoneSignal = "done"

	callerTaskQueue  = "nexusOpQueryWorkflowCallerTQ"
	handlerTaskQueue = "nexusOpQueryWorkflowHandlerTQ"
)

type queryInput struct {
	WorkflowID, RunID string
	Fail              bool
	Delay             bool

	Timeout time.Duration
}

func (ts *IntegrationTestSuite) TestNexusQueryWorkflowOperation() {
	// TODO: Re-enable this test once a cli version with the server change is tagged
	ts.T().SkipNow()
	ctx, cancel := context.WithTimeout(context.TODO(), nexusQueryTestTimeout)
	defer cancel()

	queryOp, err := temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[queryInput, int]{
		Name: queryOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input queryInput, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[int], error) {
			handle, err := nc.GetWorkflowClient().QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
				WorkflowID:           input.WorkflowID,
				RunID:                input.RunID,
				QueryType:            queryOp,
				QueryRejectCondition: enumspb.QUERY_REJECT_CONDITION_NOT_OPEN,
				Args:                 []any{input.Fail, input.Delay},
			})
			if err != nil {
				return temporalnexus.TemporalOperationResult[int]{}, err
			}
			if handle.QueryRejected != nil {
				return temporalnexus.TemporalOperationResult[int]{}, &nexus.OperationError{
					State:   nexus.OperationStateFailed,
					Message: fmt.Sprintf("query rejected with status: %s", handle.QueryRejected.GetStatus()),
				}
			}
			var count int
			if err := handle.QueryResult.Get(&count); err != nil {
				return temporalnexus.TemporalOperationResult[int]{}, &nexus.OperationError{
					State:   nexus.OperationStateFailed,
					Message: err.Error(),
				}
			}
			return temporalnexus.NewSyncResult(count), nil
		},
	})
	ts.NoError(err)

	endpoint := "query-workflow-backed-nexus-ep-" + uuid.NewString()
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

	callerWorkflow := func(ctx workflow.Context, input queryInput) (int, error) {
		nc := workflow.NewNexusClient(endpoint, queryService)
		opts := workflow.NexusOperationOptions{
			Summary: "Query Operation",
		}
		if input.Timeout != 0 {
			// to simulate errors on long-running queries that should fail correctly
			opts.ScheduleToCloseTimeout = input.Timeout
		}
		fut := nc.ExecuteOperation(ctx, queryOp, input, opts)
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return 0, err
		}
		var out int
		if err := fut.Get(ctx, &out); err != nil {
			return 0, err
		}
		return out, nil
	}

	callerWorker := worker.New(ts.client, callerTaskQueue, worker.Options{})
	callerWorker.RegisterWorkflow(callerWorkflow)
	ts.NoError(callerWorker.Start())
	defer callerWorker.Stop()

	// do not start the handlerWorker yet - to simulate case where the worker is delayed
	handlerWorker := worker.New(ts.client, handlerTaskQueue, worker.Options{})
	service := nexus.NewService(queryService)
	ts.NoError(service.Register(queryOp))
	handlerWorker.RegisterNexusService(service)
	handlerWorker.RegisterWorkflow(counterWorkflow)

	counterWorkflowID := "counter-" + uuid.NewString()
	counterWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        counterWorkflowID,
		TaskQueue: handlerTaskQueue,
	}, counterWorkflow)
	ts.NoError(err)

	stopCounterWorkflow := func() {
		ts.NoError(ts.client.SignalWorkflow(ctx, counterWorkflowID, "", queryDoneSignal, nil))
		ts.NoError(counterWorkflowRun.Get(ctx, nil))
	}

	ts.Run("Verify delayed operations complete successfully", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "delayed-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID})
		ts.Require().NoError(err)

		// now, start the worker - op should complete
		ts.NoError(handlerWorker.Start())

		var count int
		ts.NoError(queryWorkflowRun.Get(ctx, &count))
		ts.Assert().Equal(count, 1)
	})
	defer handlerWorker.Stop()

	ts.Run("Verify queries on unknown WorkflowIDs fail", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "unknown-wid-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: "unknown-wid"})
		ts.Require().NoError(err)
		ts.Require().Error(queryWorkflowRun.Get(ctx, nil))
	})

	ts.Run("Verify queries on unknown RunIDs fail", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "unknown-rid-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID, RunID: "unknown-rid"})
		ts.Require().NoError(err)
		ts.Require().Error(queryWorkflowRun.Get(ctx, nil))
	})

	ts.Run("Verify operation fails if query failures are propagated", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "unsuccessful-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID, Fail: true})
		ts.Require().NoError(err)
		ts.Require().Error(queryWorkflowRun.Get(ctx, nil))
	})

	ts.Run("Verify operation happy path has links set ", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "successful-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID})
		ts.Require().NoError(err)
		var count int
		ts.NoError(queryWorkflowRun.Get(ctx, &count))
		ts.Assert().Equal(count, 1)
		forwardLink := &common.Link{Variant: &common.Link_Workflow_{Workflow: &common.Link_Workflow{
			Namespace:  ts.config.Namespace,
			WorkflowId: counterWorkflowID,
			RunId:      counterWorkflowRun.GetRunID(),
			Reason:     "Query processed",
		}}}
		eventsFilter := func(e *history.HistoryEvent) bool {
			filterOpTypes := []enumspb.EventType{
				enumspb.EVENT_TYPE_NEXUS_OPERATION_COMPLETED,
			}
			return slices.Contains(filterOpTypes, e.EventType)
		}
		callerWorkflowLinks := getEventLinks(ctx, ts.client, queryWorkflowRun, eventsFilter)
		ts.True(checkForLink(callerWorkflowLinks, forwardLink))
	})

	ts.Run("Verify long running queries fail", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "timed-out-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID, Delay: true, Timeout: 5 * time.Second})
		ts.Require().NoError(err)
		ts.Require().Error(queryWorkflowRun.Get(ctx, nil))
	})

	ts.Run("Verify query rejection conditions are honored", func() {
		// attempt running on workflow thats not open- should fail
		stopCounterWorkflow()
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "rejected-query-" + uuid.NewString(),
			TaskQueue:           callerTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID})
		ts.NoError(err)
		ts.Error(queryWorkflowRun.Get(ctx, nil))
	})
}
