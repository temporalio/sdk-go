package cadence

// All code in this file is private to the package.

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
)

type (
	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	workflowWorker struct {
		executionParameters WorkerExecutionParameters
		workflowService     m.TChanWorkflowService
		poller              taskPoller // taskPoller to poll the tasks.
		worker              *baseWorker
		identity            string
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
	}

	// Worker overrides.
	workerOverrides struct {
		workflowTaskHander  WorkflowTaskHandler
		activityTaskHandler ActivityTaskHandler
	}
)

// NewWorkflowTaskWorker returns an instance of a workflow task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewWorkflowTaskWorker(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) (worker Lifecycle) {
	return newWorkflowTaskWorkerInternal(taskHandler, service, params)
}

// newWorkflowWorker returns an instance of the workflow worker.
func newWorkflowWorker(
	factory workflowDefinitionFactory,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	ppMgr pressurePointMgr,
) Lifecycle {
	return newWorkflowWorkerInternal(factory, service, params, ppMgr, nil)
}

func getWorkerIdentityForParams(params WorkerExecutionParameters) string {
	identity := params.Identity
	if identity == "" {
		identity = getWorkerIdentity(params.TaskList)
	}
	return identity
}

func newWorkflowWorkerInternal(
	factory workflowDefinitionFactory,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	ppMgr pressurePointMgr,
	overrides *workerOverrides,
) Lifecycle {
	// Get a workflow task handler.
	var taskHandler WorkflowTaskHandler
	if overrides != nil && overrides.workflowTaskHander != nil {
		taskHandler = overrides.workflowTaskHander
	} else {
		taskHandler = newWorkflowTaskHandler(factory, params, ppMgr)
	}
	return newWorkflowTaskWorkerInternal(taskHandler, service, params)
}

func newWorkflowTaskWorkerInternal(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) Lifecycle {
	poller := newWorkflowTaskPoller(
		taskHandler,
		service,
		params,
	)
	identity := getWorkerIdentityForParams(params)

	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    params.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        identity,
		workerType:      "DecisionWorker"},
		params.Logger)

	return &workflowWorker{
		executionParameters: params,
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

func newActivityWorkerInternal(
	activities []Activity,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	overrides *workerOverrides,
) Lifecycle {

	// Get a activity task handler.
	var taskHandler ActivityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(activities, service, params)
	}
	return NewActivityTaskWorker(taskHandler, service, params)
}

// NewActivityTaskWorker returns instance of an activity task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewActivityTaskWorker(
	taskHandler ActivityTaskHandler,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) Lifecycle {
	identity := getWorkerIdentityForParams(params)

	poller := newActivityTaskPoller(
		taskHandler,
		service,
		params,
	)
	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    params.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        identity,
		workerType:      "ActivityWorker"},
		params.Logger)

	return &activityWorker{
		executionParameters: params,
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
