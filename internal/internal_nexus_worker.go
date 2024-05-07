package internal

import (
	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/workflowservice/v1"
)

type nexusWorkerOptions struct {
	executionParameters workerExecutionParameters
	client              Client
	workflowService     workflowservice.WorkflowServiceClient
	handler             nexus.Handler
}

type nexusWorker struct {
	executionParameters workerExecutionParameters
	workflowService     workflowservice.WorkflowServiceClient
	worker              *baseWorker
	stopC               chan struct{}
}

func newNexusWorker(opts nexusWorkerOptions) (*nexusWorker, error) {
	workerStopChannel := make(chan struct{})
	params := opts.executionParameters
	params.WorkerStopChannel = getReadOnlyChannel(workerStopChannel)
	ensureRequiredParams(&params)
	poller := newNexusTaskPoller(
		newNexusTaskHandler(
			opts.handler,
			opts.executionParameters.TaskQueue,
			opts.client,
			opts.executionParameters.DataConverter,
			&workflowservice.RespondNexusTaskCompletedRequest{
				Namespace: opts.executionParameters.Namespace,
				Identity:  opts.executionParameters.Identity,
			},
			&workflowservice.RespondNexusTaskFailedRequest{
				Namespace: opts.executionParameters.Namespace,
				Identity:  opts.executionParameters.Identity,
			},
			opts.executionParameters.Logger,
			opts.executionParameters.MetricsHandler,
		),
		opts.workflowService,
		params,
	)

	baseWorker := newBaseWorker(baseWorkerOptions{
		pollerCount:       params.MaxConcurrentNexusTaskQueuePollers,
		pollerRate:        defaultPollerRate,
		maxConcurrentTask: params.ConcurrentNexusTaskExecutionSize,
		maxTaskPerSecond:  defaultWorkerTaskExecutionRate,
		taskWorker:        poller,
		identity:          params.Identity,
		workerType:        "NexusWorker",
		stopTimeout:       params.WorkerStopTimeout,
		fatalErrCb:        params.WorkerFatalErrorCallback,
	},
		params.Logger,
		params.MetricsHandler,
		nil,
	)

	return &nexusWorker{
		executionParameters: opts.executionParameters,
		workflowService:     opts.workflowService,
		worker:              baseWorker,
		stopC:               workerStopChannel,
	}, nil
}

// Start the worker.
func (w *nexusWorker) Start() error {
	err := verifyNamespaceExist(w.workflowService, w.executionParameters.MetricsHandler, w.executionParameters.Namespace, w.worker.logger)
	if err != nil {
		return err
	}
	w.worker.Start()
	return nil
}

// Stop the worker.
func (w *nexusWorker) Stop() {
	close(w.stopC)
	w.worker.Stop()
}
