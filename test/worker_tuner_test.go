package test_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/worker"
)

type WorkerTunerTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	activities *Activities
}

func TestWorkerTunerTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerTunerTestSuite))
}

func (ts *WorkerTunerTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = &Activities{}
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkerTunerTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkerTunerTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerTunerTestSuite) TestFixedSizeWorkerTuner() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	tuner, err := worker.NewFixedSizeTuner(worker.FixedSizeTunerOptions{
		NumWorkflowSlots: 10, NumActivitySlots: 10, NumLocalActivitySlots: 5,
	})
	ts.NoError(err)

	ts.runTheWorkflow(worker.Options{Tuner: tuner}, ctx)
}

func (ts *WorkerTunerTestSuite) TestCompositeWorkerTuner() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfSS, err := worker.NewFixedSizeSlotSupplier(10)
	ts.NoError(err)
	controllerOpts := worker.DefaultResourceControllerOptions()
	controllerOpts.MemTargetPercent = 0.8
	controllerOpts.CpuTargetPercent = 0.9
	controller := worker.NewResourceController(controllerOpts)
	actSS, err := worker.NewResourceBasedSlotSupplier(controller,
		worker.ResourceBasedSlotSupplierOptions{
			MinSlots:     10,
			MaxSlots:     20,
			RampThrottle: 0,
		})
	ts.NoError(err)
	laCss, err := worker.NewFixedSizeSlotSupplier(5)
	ts.NoError(err)
	tuner, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier: wfSS, ActivitySlotSupplier: actSS, LocalActivitySlotSupplier: laCss})
	ts.NoError(err)

	ts.runTheWorkflow(worker.Options{Tuner: tuner}, ctx)
}

func (ts *WorkerTunerTestSuite) TestPollerBehaviorAutoscalingScaler() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	ts.runTheWorkflow(worker.Options{
		WorkflowTaskPollerBehavior: worker.NewPollerBehaviorAutoscaling(
			worker.PollerBehaviorAutoscalingOptions{},
		),
		ActivityTaskPollerBehavior: worker.NewPollerBehaviorAutoscaling(
			worker.PollerBehaviorAutoscalingOptions{},
		),
	}, ctx)
}

func (ts *WorkerTunerTestSuite) TestPollerBehaviorSimpleMaximumScaler() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	ts.runTheWorkflow(worker.Options{
		WorkflowTaskPollerBehavior: worker.NewPollerBehaviorSimpleMaximum(
			worker.PollerBehaviorSimpleMaximumOptions{},
		),
		ActivityTaskPollerBehavior: worker.NewPollerBehaviorSimpleMaximum(
			worker.PollerBehaviorSimpleMaximumOptions{},
		),
	}, ctx)
}

func (ts *WorkerTunerTestSuite) TestResourceBasedSmallSlots() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfSS, err := worker.NewFixedSizeSlotSupplier(10)
	ts.NoError(err)
	controllerOpts := worker.DefaultResourceControllerOptions()
	controllerOpts.MemTargetPercent = 0.8
	controllerOpts.CpuTargetPercent = 0.9
	controller := worker.NewResourceController(controllerOpts)
	actSS, err := worker.NewResourceBasedSlotSupplier(controller,
		worker.ResourceBasedSlotSupplierOptions{
			MinSlots:     1,
			MaxSlots:     4,
			RampThrottle: 0,
		})
	ts.NoError(err)
	laCss, err := worker.NewFixedSizeSlotSupplier(5)
	ts.NoError(err)
	tuner, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier: wfSS, ActivitySlotSupplier: actSS, LocalActivitySlotSupplier: laCss})
	ts.NoError(err)

	// The bug this is verifying was triggered by a race, so run this a bunch to verify it's not hit
	for i := 0; i < 10; i++ {
		ts.runTheWorkflow(worker.Options{Tuner: tuner}, ctx)
	}
}

func (ts *WorkerTunerTestSuite) runTheWorkflow(workerOptions worker.Options, ctx context.Context) {
	myWorker := worker.New(ts.client, ts.taskQueueName, workerOptions)
	ts.workflows.register(myWorker)
	ts.activities.register(myWorker)
	ts.NoError(myWorker.Start())
	defer myWorker.Stop()

	handle, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		ts.workflows.RunsLocalAndNonlocalActsWithRetries, 5, 2)
	ts.NoError(err)
	ts.NoError(handle.Get(ctx, nil))
}
