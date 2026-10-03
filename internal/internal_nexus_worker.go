// After earlier children start, SDK startup calls Nexus instrumentation on the
// caller's stack, then constructs and stores this child while holding Stop's
// mutex. If Stop wins, startup returns shutdown without allocating SDK contexts.
package internal

import (
	"github.com/nexus-rpc/sdk-go/nexus"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/common/metrics"
)

type (
	nexusWorkerOptions struct {
		executionParameters workerExecutionParameters
		client              Client
		workflowService     workflowservice.WorkflowServiceClient
		handler             nexus.Handler
		registry            *registry
		baseWorker          *baseWorker
		numPollerMetric     *numPollerMetric
	}

	nexusWorker struct {
		executionParameters workerExecutionParameters
		workflowService     workflowservice.WorkflowServiceClient
		worker              *baseWorker
		// stopC is created by newNexusWorker, exposed to the Nexus task poller through
		// WorkerStopChannel, and closed by nexusWorker.Stop() during shutdown.
		stopC chan struct{}
	}
)

// Nexus options supply the poller metrics, tuner, and base instrumentation in
// that order. prepareNexusWorker returns their saved values without allocating
// SDK contexts or stop channels, so each callback can Stop and return.
func prepareNexusWorker(opts nexusWorkerOptions) nexusWorkerOptions {
	params := opts.executionParameters
	ensureRequiredParams(&params)
	opts.executionParameters = params
	opts.numPollerMetric = newNumPollerMetric(params.MetricsHandler, metrics.PollerTypeNexusTask)
	slotSupplier := params.Tuner.GetNexusSlotSupplier()
	opts.baseWorker = prepareBaseWorker(baseWorkerOptions{
		pollerRate:                   defaultPollerRate,
		slotSupplier:                 slotSupplier,
		maxTaskPerSecond:             defaultWorkerTaskExecutionRate,
		workerType:                   "NexusWorker",
		identity:                     params.Identity,
		buildId:                      params.getBuildID(),
		logger:                       params.Logger,
		stopTimeout:                  params.WorkerStopTimeout,
		fatalErrCb:                   params.WorkerFatalErrorCallback,
		metricsHandler:               params.MetricsHandler,
		workerPollCompleteOnShutdown: params.workerPollCompleteOnShutdown,
		slotReservationData: slotReservationData{
			taskQueue:     params.TaskQueue,
			taskQueueKind: enumspb.TASK_QUEUE_KIND_NORMAL,
		},
		isInternalWorker: params.isInternalWorker(),
	})
	return opts
}

// Prepared instrumentation lets newNexusWorker construct the complete child
// without custom callbacks. Its caller holds Stop's mutex until assignment;
// if construction panics after allocating contexts, cancel them before unwinding.
func newNexusWorker(opts nexusWorkerOptions) *nexusWorker {
	workerStopChannel := make(chan struct{})
	params := opts.executionParameters
	params.WorkerStopChannel = getReadOnlyChannel(workerStopChannel)
	var pollerGroups *pollerGroupManager
	if _, ok := params.NexusTaskPollerBehavior.(*pollerBehaviorAutoscaling); ok {
		pollerGroups = newPollerGroupManager(params.pollerGroupSnapshotStore)
	}
	poller := newNexusTaskPoller(
		newNexusTaskHandler(
			opts.handler,
			opts.executionParameters.Identity,
			opts.executionParameters.Namespace,
			opts.executionParameters.TaskQueue,
			opts.client,
			opts.executionParameters.DataConverter,
			opts.executionParameters.FailureConverter,
			opts.executionParameters.Logger,
			opts.executionParameters.MetricsHandler,
			opts.registry,
		),
		opts.workflowService,
		params,
		pollerGroups,
		opts.numPollerMetric,
	)

	base := opts.baseWorker
	base.options.taskProcessor = poller
	base.options.taskPollers = []scalableTaskPoller{
		newScalableTaskPoller(
			poller,
			opts.executionParameters.Logger,
			params.NexusTaskPollerBehavior,
			metrics.PollerTypeNexusTask,
			params.serverSupportsAutoscaling,
			pollerGroups,
		),
	}
	base.initializeResources()
	complete := false
	defer func() {
		if !complete {
			base.limiterContextCancel()
			base.taskLimiterContextCancel()
		}
	}()
	worker := &nexusWorker{
		executionParameters: opts.executionParameters,
		workflowService:     opts.workflowService,
		worker:              base,
		stopC:               workerStopChannel,
	}
	complete = true
	return worker
}

// Stop the worker.
func (w *nexusWorker) Stop() {
	close(w.stopC)
	w.worker.Stop()
}
