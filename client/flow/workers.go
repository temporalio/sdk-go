package flow

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"code.uber.internal/go-common.git/x/log"
	"github.com/uber-common/bark"
	"github.com/uber/tchannel-go/thrift"
)

// PressurePoints
const (
	PressurePointTypeDecisionTaskStartTimeout = "decision-task-start-timeout"
	PressurePointConfigProbability            = "probability"
)

type (
	// WorkerExecutionParameters defines worker configure/execution options.
	WorkerExecutionParameters struct {
		// Task list name to poll.
		TaskListName string

		// Defines how many concurrent poll requests for the task list by this worker.
		ConcurrentPollRoutineSize int

		// Defines how many executions for task list by this worker.
		// TODO: In future we want to separate the activity executions as they take longer than polls.
		// ConcurrentExecutionRoutineSize int

		// User can provide an identity for the debuggability. If not provided the framework has
		// a default option.
		Identity string
	}

	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	WorkflowWorker struct {
		executionParameters WorkerExecutionParameters
		workflowDefFactory  WorkflowDefinitionFactory
		workflowService     m.TChanWorkflowService
		poller              taskPoller // taskPoller to poll the tasks.
		worker              *baseWorker
		identity            string
		logger              bark.Logger
	}

	// activityRegistry collection of activity implementations
	activityRegistry map[string]ActivityImplementation

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	ActivityWorker struct {
		executionParameters WorkerExecutionParameters
		activityRegistry    activityRegistry
		workflowService     m.TChanWorkflowService
		poller              *activityTaskPoller
		worker              *baseWorker
		identity            string
		logger              bark.Logger
	}

	// Worker overrides.
	workerOverrides struct {
		workflowTaskHander  workflowTaskHandler
		activityTaskHandler activityTaskHandler
	}
)

// NewWorkflowWorker returns an instance of the workflow worker.
func NewWorkflowWorker(params WorkerExecutionParameters, factory WorkflowDefinitionFactory,
	service m.TChanWorkflowService, logger bark.Logger,
	reporter metrics.Reporter, pressurePoints map[string]map[string]string) *WorkflowWorker {
	return newWorkflowWorkerInternal(params, factory, service, logger, reporter, pressurePoints, nil)
}

func newWorkflowWorkerInternal(params WorkerExecutionParameters, factory WorkflowDefinitionFactory,
	service m.TChanWorkflowService, logger bark.Logger, reporter metrics.Reporter,
	pressurePoints map[string]map[string]string, overrides *workerOverrides) *WorkflowWorker {
	// Get an identity.
	identity := params.Identity
	if identity == "" {
		identity = GetWorkerIdentity(params.TaskListName)
	}

	// Get a workflow task handler.
	var taskHandler workflowTaskHandler
	if overrides != nil && overrides.workflowTaskHander != nil {
		taskHandler = overrides.workflowTaskHander
	} else {
		taskHandler = newWorkflowTaskHandler(params.TaskListName, identity, factory, logger, reporter, pressurePoints)
	}

	poller := newWorkflowTaskPoller(
		service,
		params.TaskListName,
		identity,
		taskHandler,
		logger,
		reporter)
	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    params.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        identity})

	return &WorkflowWorker{
		executionParameters: params,
		workflowDefFactory:  factory,
		workflowService:     service,
		poller:              poller,
		worker:              worker,
		identity:            identity,
	}
}

// Start the worker.
func (ww *WorkflowWorker) Start() {
	ww.worker.Start()
}

// Shutdown the worker.
func (ww *WorkflowWorker) Shutdown() {
	ww.worker.Shutdown()
}

// NewActivityWorker returns an instance of the activity worker.
func NewActivityWorker(executionParameters WorkerExecutionParameters, factory ActivityImplementationFactory,
	service m.TChanWorkflowService, logger bark.Logger, reporter metrics.Reporter) *ActivityWorker {
	return newActivityWorkerInternal(executionParameters, factory, service, logger, reporter, nil)
}

func newActivityWorkerInternal(executionParameters WorkerExecutionParameters, factory ActivityImplementationFactory,
	service m.TChanWorkflowService, logger bark.Logger, reporter metrics.Reporter, overrides *workerOverrides) *ActivityWorker {
	// Get an identity.
	identity := executionParameters.Identity
	if identity == "" {
		identity = GetWorkerIdentity(executionParameters.TaskListName)
	}

	if logger == nil {
		logger = log.WithFields(log.Fields{tagTaskListName: executionParameters.TaskListName})
	}

	// Get a activity task handler.
	var taskHandler activityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(executionParameters.TaskListName, executionParameters.Identity,
			factory, service, logger, reporter)
	}
	poller := newActivityTaskPoller(
		service,
		executionParameters.TaskListName,
		identity,
		taskHandler,
		reporter,
		logger)
	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    executionParameters.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        identity})

	return &ActivityWorker{
		executionParameters: executionParameters,
		activityRegistry:    make(map[string]ActivityImplementation),
		workflowService:     service,
		worker:              worker,
		poller:              poller,
		identity:            identity,
	}
}

// Start the worker.
func (aw *ActivityWorker) Start() {
	aw.worker.Start()
}

// Shutdown the worker.
func (aw *ActivityWorker) Shutdown() {
	aw.worker.Shutdown()
}

// NewWorkflowClient creates an instance of workflow client that users can start a workflow
func NewWorkflowClient(options StartWorkflowOptions, service m.TChanWorkflowService, reporter metrics.Reporter) *WorkflowClient {
	// Get an identity.
	identity := options.Identity
	if identity == "" {
		identity = GetWorkerIdentity(options.TaskListName)
	}
	return &WorkflowClient{options: options, workflowService: service, Identity: identity, reporter: reporter}
}

// StartWorkflowExecution starts a workflow execution
func (wc *WorkflowClient) StartWorkflowExecution() (*WorkflowExecution, error) {

	startRequest := &s.StartWorkflowExecutionRequest{
		WorkflowId:   common.StringPtr(wc.options.WorkflowID),
		WorkflowType: WorkflowTypePtr(wc.options.WorkflowType),
		TaskList:     common.TaskListPtr(s.TaskList{Name: common.StringPtr(wc.options.TaskListName)}),
		Input:        wc.options.WorkflowInput,
		ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(wc.options.ExecutionStartToCloseTimeoutSeconds),
		TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(wc.options.DecisionTaskStartToCloseTimeoutSeconds),
		Identity:                            common.StringPtr(wc.Identity)}

	var response *s.StartWorkflowExecutionResponse

	// Start creating workflow request.
	err := backoff.Retry(
		func() error {
			ctx, cancel := thrift.NewContext(serviceTimeOut)
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.StartWorkflowExecution(ctx, startRequest)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return nil, err
	}

	if wc.reporter != nil {
		wc.reporter.IncCounter(metrics.WorkflowsStartTotalCounter, nil, 1)
	}

	executionInfo := &WorkflowExecution{
		WorkflowID: wc.options.WorkflowID,
		RunID:      response.GetRunId()}
	return executionInfo, nil
}
