package test_test

import (
	"context"
	"os"
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

	queryTaskQueue = "nexusOpUpdateWorkflowTQ"
)

type queryInput struct {
	WorkflowID string
}

func (ts *IntegrationTestSuite) TestNexusQueryWorkflowOperation() {
	if os.Getenv("DISABLE_STANDALONE_NEXUS_TESTS") != "" {
		ts.T().SkipNow()
	}
	ctx, cancel := context.WithTimeout(context.TODO(), nexusQueryTestTimeout)
	defer cancel()

	queryOp, err := temporalnexus.NewTemporalOperation(temporalnexus.TemporalOperationOptions[queryInput, int]{
		Name: queryOperation,
		Start: func(ctx context.Context, nc temporalnexus.NexusClient, input queryInput, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[int], error) {
			return temporalnexus.StartQueryWorkflow[int](ctx, nc, client.QueryWorkflowWithOptionsRequest{
				WorkflowID:           input.WorkflowID,
				QueryType:            queryOp,
				QueryRejectCondition: enumspb.QUERY_REJECT_CONDITION_NOT_OPEN,
			})
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
						TaskQueue: queryTaskQueue,
					},
				},
			},
		},
	})
	ts.NoError(err)

	callerWorkflow := func(ctx workflow.Context, input queryInput) (int, error) {
		nc := workflow.NewNexusClient(endpoint, queryService)
		fut := nc.ExecuteOperation(ctx, queryOp, input, workflow.NexusOperationOptions{
			Summary: "Query Operation",
		})
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

	w := worker.New(ts.client, queryTaskQueue, worker.Options{})
	service := nexus.NewService(queryService)
	ts.NoError(service.Register(queryOp))

	w.RegisterNexusService(service)
	w.RegisterWorkflow(counterWorkflow)
	w.RegisterWorkflow(callerWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()

	counterWorkflowID := "counter-" + uuid.NewString()
	counterWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        counterWorkflowID,
		TaskQueue: queryTaskQueue,
	}, counterWorkflow)
	ts.NoError(err)

	stopCounterWorkflow := func() {
		ts.NoError(ts.client.SignalWorkflow(ctx, counterWorkflowID, "", queryDoneSignal, nil))
		ts.NoError(counterWorkflowRun.Get(ctx, nil))
	}

	ts.Run("Verify operation happy path has links set ", func() {
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "successful-query-" + uuid.NewString(),
			TaskQueue:           queryTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID})
		ts.NoError(err)
		var count int
		ts.NoError(queryWorkflowRun.Get(ctx, &count))
		ts.Assert().Equal(count, 1)
		forwardLink := &common.Link{Variant: &common.Link_Workflow_{Workflow: &common.Link_Workflow{
			Namespace:  ts.config.Namespace,
			WorkflowId: counterWorkflowRun.GetID(),
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

	ts.Run("Verify query rejection conditions are honored", func() {
		// attempt running on workflow thats not open- should fail
		stopCounterWorkflow()
		queryWorkflowRun, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                  "rejected-query-" + uuid.NewString(),
			TaskQueue:           queryTaskQueue,
			WorkflowTaskTimeout: time.Second,
		}, callerWorkflow, queryInput{WorkflowID: counterWorkflowID})
		ts.NoError(err)
		ts.Error(queryWorkflowRun.Get(ctx, nil))
	})
}
