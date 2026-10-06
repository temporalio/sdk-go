package test_test

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	workflowRandomName              = "go.temporal.io/sdk/test"
	workflowRandomQueryCurrentState = "current_state"
)

type WorkflowRandomTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	worker worker.Worker
}

func TestWorkflowRandomTestSuite(t *testing.T) {
	suite.Run(t, new(WorkflowRandomTestSuite))
}

func (ts *WorkflowRandomTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkflowRandomTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkflowRandomTestSuite) SetupTest() {
	ts.Assertions = require.New(ts.T())
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.worker.RegisterWorkflow(workflowRandomSimpleWorkflow)
	ts.worker.RegisterWorkflow(workflowRandomReplayWorkflow)
	ts.worker.RegisterWorkflow(workflowRandomResetWorkflow)
	ts.worker.RegisterWorkflow(workflowRandomResetLateSourceWorkflow)
	ts.worker.RegisterWorkflow(workflowRandomContinueAsNewWorkflow)
	ts.worker.RegisterActivity(workflowRandomSimpleActivity)
	ts.NoError(ts.worker.Start())
}

func (ts *WorkflowRandomTestSuite) TearDownTest() {
	ts.worker.Stop()
}

func workflowRandomSimpleWorkflow(ctx workflow.Context) (int, error) {
	return rand.New(workflow.GetRandomStream(ctx, workflowRandomName)).Int(), nil
}

func workflowRandomReplayWorkflow(ctx workflow.Context) (uint64, error) {
	var state uint64 = 0

	workflow.SetQueryHandler(ctx, workflowRandomQueryCurrentState, func() (uint64, error) {
		return state, nil
	})

	c := workflow.GetRandomStream(ctx, workflowRandomName)

	state = c.Uint64()

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	if err := workflow.ExecuteActivity(ctx, workflowRandomSimpleActivity).Get(ctx, nil); err != nil {
		return 0, err
	}

	state = c.Uint64()

	return state, nil
}

func workflowRandomSimpleActivity(context.Context) error {
	return nil
}

func workflowRandomResetWorkflow(ctx workflow.Context) ([]int, error) {
	r := rand.New(workflow.GetRandomStream(ctx, workflowRandomName))

	first := r.Int()

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	if err := workflow.ExecuteActivity(ctx, workflowRandomSimpleActivity).Get(ctx, nil); err != nil {
		return nil, err
	}

	// The reset targets the second WFT (id=10), so the first draw is replayed and the second draw is redrawn.
	second := r.Int()

	return []int{first, second}, nil
}

func workflowRandomResetLateSourceWorkflow(ctx workflow.Context) ([]int, error) {
	first := rand.New(workflow.GetRandomStream(ctx, "other")).Int()

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	if err := workflow.ExecuteActivity(ctx, workflowRandomSimpleActivity).Get(ctx, nil); err != nil {
		return nil, err
	}

	// The reset targets the second WFT (id=10), so the first draw is replayed and the second draw is redrawn.
	second := rand.New(workflow.GetRandomStream(ctx, workflowRandomName)).Int()

	return []int{first, second}, nil
}

func workflowRandomContinueAsNewWorkflow(ctx workflow.Context, prev int) ([]int, error) {
	current := rand.New(workflow.GetRandomStream(ctx, workflowRandomName)).Int()

	if prev == 0 {
		return nil, workflow.NewContinueAsNewError(ctx, workflowRandomContinueAsNewWorkflow, current)
	}

	return []int{prev, current}, nil
}

func (ts *WorkflowRandomTestSuite) TestNoCollisionAcrossRuns() {
	var a, b int
	ts.NoError(ts.executeWorkflow(ts.T().Name(), workflowRandomSimpleWorkflow, &a))
	ts.NoError(ts.executeWorkflow(ts.T().Name(), workflowRandomSimpleWorkflow, &b))
	ts.NotEqual(a, b)
}

func (ts *WorkflowRandomTestSuite) TestDeterministicReplay() {
	ctx := context.Background()
	wfID := ts.T().Name()

	var a uint64
	run, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions(wfID), workflowRandomReplayWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, &a))

	var b uint64
	queryResult, err := ts.client.QueryWorkflow(ctx, wfID, run.GetRunID(), workflowRandomQueryCurrentState)
	ts.NoError(err)
	ts.NoError(queryResult.Get(&b))

	ts.Equal(a, b)
}

func (ts *WorkflowRandomTestSuite) TestResetReproducesValues() {
	ctx := context.Background()
	wfID := ts.T().Name()

	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        wfID,
		TaskQueue: ts.taskQueueName,
	}, workflowRandomResetWorkflow)
	ts.NoError(err)

	var original []int
	ts.NoError(run.Get(ctx, &original))
	ts.Len(original, 2)
	ts.NotEqual(original[0], original[1])

	resp, err := ts.client.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: ts.config.Namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: wfID,
			RunId:      run.GetRunID(),
		},
		Reason:                    "integration test",
		RequestId:                 uuid.NewString(),
		WorkflowTaskFinishEventId: 10,
	})
	ts.NoError(err)

	var afterReset []int
	ts.NoError(ts.client.GetWorkflow(ctx, wfID, resp.GetRunId()).Get(ctx, &afterReset))
	ts.Len(afterReset, 2)
	ts.NotEqual(afterReset[0], afterReset[1])

	// The values before the reset point should be the same
	ts.Equal(original[0], afterReset[0])

	// The values after the reset point should be different
	ts.NotEqual(original[1], afterReset[1])
}

func (ts *WorkflowRandomTestSuite) TestResetReseedsSourceCreatedAfterResetPoint() {
	ctx := context.Background()
	wfID := ts.T().Name()

	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        wfID,
		TaskQueue: ts.taskQueueName,
	}, workflowRandomResetLateSourceWorkflow)
	ts.NoError(err)

	var original []int
	ts.NoError(run.Get(ctx, &original))
	ts.Len(original, 2)
	ts.NotEqual(original[0], original[1])

	resp, err := ts.client.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: ts.config.Namespace,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: wfID,
			RunId:      run.GetRunID(),
		},
		Reason:                    "integration test",
		RequestId:                 uuid.NewString(),
		WorkflowTaskFinishEventId: 10,
	})
	ts.NoError(err)

	var afterReset []int
	ts.NoError(ts.client.GetWorkflow(ctx, wfID, resp.GetRunId()).Get(ctx, &afterReset))
	ts.Len(afterReset, 2)
	ts.NotEqual(afterReset[0], afterReset[1])

	// The values before the reset point should be the same
	ts.Equal(original[0], afterReset[0])

	// The values after the reset point should be different
	ts.NotEqual(original[1], afterReset[1])
}

func (ts *WorkflowRandomTestSuite) TestContinueAsNewDrawsNewValues() {
	var values []int
	ts.NoError(ts.executeWorkflow(ts.T().Name(), workflowRandomContinueAsNewWorkflow, &values, 0))

	// Each continueAsNew run should draw new values
	ts.Len(values, 2)
	ts.NotEqual(values[0], values[1])
}
