package cadence

// All code in this file is private to the package.

import (
	"github.com/uber-common/bark"
	"github.com/uber-go/tally"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
	"code.uber.internal/go-common.git/x/log"
)

type (
	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	workflowWorker struct {
		executionParameters WorkerExecutionParameters
		workflowDefFactory  workflowDefinitionFactory
		workflowService     m.TChanWorkflowService
		poller              taskPoller // taskPoller to poll the tasks.
		worker              *baseWorker
		identity            string
		logger              bark.Logger
	}

	// activityRegistry collection of activity implementations
	activityRegistry map[string]Activity

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	activityWorker struct {
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
func newWorkflowWorker(params WorkerExecutionParameters, factory workflowDefinitionFactory,
	service m.TChanWorkflowService, logger bark.Logger,
	metricsScope tally.Scope, ppMgr pressurePointMgr) *workflowWorker {
	return newWorkflowWorkerInternal(params, factory, service, logger, metricsScope, ppMgr, nil)
}

func newWorkflowWorkerInternal(params WorkerExecutionParameters, factory workflowDefinitionFactory,
	service m.TChanWorkflowService, logger bark.Logger, metricsScope tally.Scope,
	ppMgr pressurePointMgr, overrides *workerOverrides) *workflowWorker {
	// Get an identity.
	identity := params.Identity
	if identity == "" {
		identity = getWorkerIdentity(params.TaskList)
	}

	// Get a workflow task handler.
	var taskHandler workflowTaskHandler
	if overrides != nil && overrides.workflowTaskHander != nil {
		taskHandler = overrides.workflowTaskHander
	} else {
		taskHandler = NewWorkflowTaskHandler(params.TaskList, identity, factory, logger, metricsScope, ppMgr)
	}

	poller := newWorkflowTaskPoller(
		service,
		params.TaskList,
		identity,
		taskHandler,
		logger,
		metricsScope)
	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    params.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        identity,
		workerType:      "DecisionWorker"},
		logger)

	return &workflowWorker{
		executionParameters: params,
		workflowDefFactory:  factory,
		workflowService:     service,
		poller:              poller,
		worker:              worker,
		identity:            identity,
	}
}

// Start the worker.
func (ww *workflowWorker) Start() error {
	ww.worker.Start()
	return nil // TODO: propagate error
}

// Shutdown the worker.
func (ww *workflowWorker) Stop() {
	ww.worker.Stop()
}

func newActivityWorkerInternal(executionParameters WorkerExecutionParameters, activities []Activity,
	service m.TChanWorkflowService, logger bark.Logger, metricsScope tally.Scope, overrides *workerOverrides) *activityWorker {
	// Get an identity.
	identity := executionParameters.Identity
	if identity == "" {
		identity = getWorkerIdentity(executionParameters.TaskList)
	}

	if logger == nil {
		logger = log.WithFields(log.Fields{tagTaskListName: executionParameters.TaskList})
	}

	// Get a activity task handler.
	var taskHandler activityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(executionParameters.TaskList, executionParameters.Identity,
			activities, service, logger, metricsScope)
	}
	poller := newActivityTaskPoller(
		service,
		executionParameters.TaskList,
		identity,
		taskHandler,
		metricsScope,
		logger)
	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    executionParameters.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        identity,
		workerType:      "ActivityWorker"},
		logger)

	return &activityWorker{
		executionParameters: executionParameters,
		activityRegistry:    make(map[string]Activity),
		workflowService:     service,
		worker:              worker,
		poller:              poller,
		identity:            identity,
	}
}

// Start the worker.
func (aw *activityWorker) Start() error {
	aw.worker.Start()
	return nil // TODO: propagate errors
}

// Shutdown the worker.
func (aw *activityWorker) Stop() {
	aw.worker.Stop()
}
