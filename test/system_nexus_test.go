package test_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// -----------------------------------------------------------------------------
// TEST SUITE
// -----------------------------------------------------------------------------

type SystemNexusTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	worker worker.Worker
}

func TestSystemNexusSuite(t *testing.T) {
	suite.Run(t, new(SystemNexusTestSuite))
}

func (ts *SystemNexusTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ts.initConfig()
	ts.NoError(ts.InitClient(func(options *client.Options) {
		options.DataConverter = converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(), &intTestSigningCodec{},
		)
	}))
	ts.NoError(ts.InitNamespace())
}

func (ts *SystemNexusTestSuite) SetupTest() {
	ts.Assertions = require.New(ts.T())
	worker.SetStickyWorkflowCacheSize(ts.config.maxWorkflowCacheSize)
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		WorkflowPanicPolicy: worker.FailWorkflow,
	})
	ts.worker.RegisterWorkflow(RepeatedlySignalAndStart)
	ts.worker.RegisterWorkflow(RepeatedlyReceiveSignal)
	ts.NoError(ts.worker.Start())
}

func (ts *SystemNexusTestSuite) TearDownTest() {
	if ts.worker != nil {
		ts.worker.Stop()
		ts.worker = nil
	}
}

func (ts *SystemNexusTestSuite) TearDownSuite() {
	if ts.client != nil {
		ts.client.Close()
		ts.client = nil
	}
}

// -----------------------------------------------------------------------------
// TESTS
// -----------------------------------------------------------------------------

// TestWorkflowSignalWithStart executes [RepeatedlySignalAndStart], which uses
// signal-with-start to signal [RepeatedlyReceiveSignal] several times.
// We expect the signalled workflow to start automatically and return
// the signals back to us.
func (ts *SystemNexusTestSuite) TestWorkflowSignalWithStart() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	id := uuid.NewString()
	callerID := ts.T().Name() + "-caller-" + id
	calleeID := ts.T().Name() + "-callee-" + id
	numCalls := 5

	// The responses from each call are written into the responses array.
	var responses []workflow.SignalWithStartWorkflowResponse
	ts.NoError(ts.executeWorkflow(executeWorkflowParams{
		ID:       callerID,
		Workflow: RepeatedlySignalAndStart,
		Args:     []any{calleeID, numCalls},
		Result:   &responses,
	}))

	// Expect 5 responses, all with the same ID. The first response also started the workflow.
	ts.Len(responses, numCalls)
	for i := range responses {
		if i == 0 {
			ts.True(*responses[0].Started)
			continue
		}
		ts.False(*responses[i].Started)
		ts.Equal(*responses[0].RunID, *responses[i].RunID)
	}

	// The callee returns an array with all its signals.
	var wantSignals, gotSignals []int
	ts.NoError(ts.client.GetWorkflow(ctx, calleeID, *responses[0].RunID).Get(ctx, &gotSignals))
	for i := range responses {
		wantSignals = append(wantSignals, i)
	}
	ts.Equal(wantSignals, gotSignals)
}

func RepeatedlySignalAndStart(
	ctx workflow.Context,
	calleeID string,
	count int,
) ([]workflow.SignalWithStartWorkflowResponse, error) {
	var responses []workflow.SignalWithStartWorkflowResponse
	for i := range count {
		var response workflow.SignalWithStartWorkflowResponse
		err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
			ID: calleeID,
			TaskQueue: workflow.GetInfo(ctx).TaskQueueName,
			WorkflowExecutionTimeout: 20 * time.Second,
		}, "signal", i, RepeatedlyReceiveSignal, count).Get(ctx, &response)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}

func RepeatedlyReceiveSignal(ctx workflow.Context, count int) ([]int, error) {
	var signals []int
	ch := workflow.GetSignalChannel(ctx, "signal")
	for range count {
		var signal int
		if !ch.Receive(ctx, &signal) {
			return nil, ctx.Err()
		}
		signals = append(signals, signal)
	}
	return signals, nil
}