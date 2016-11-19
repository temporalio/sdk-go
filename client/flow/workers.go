package flow

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
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
		poller              TaskPoller // TaskPoller to poll the tasks.
		worker              *baseWorker
		identity            string
	}

	// ActivityRegistry collection of activity implementations
	ActivityRegistry map[string]ActivityImplementation

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	ActivityWorker struct {
		executionParameters WorkerExecutionParameters
		activityRegistry    ActivityRegistry
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

// NewWorkflowWorker returns an instance of the workflow worker.
func NewWorkflowWorker(params WorkerExecutionParameters, factory WorkflowDefinitionFactory, service m.TChanWorkflowService) *WorkflowWorker {
	return newWorkflowWorkerInternal(params, factory, service, nil)
}

func newWorkflowWorkerInternal(params WorkerExecutionParameters, factory WorkflowDefinitionFactory,
	service m.TChanWorkflowService, overrides *workerOverrides) *WorkflowWorker {
	var taskHandler WorkflowTaskHandler // = &WorkflowTaskHandler{}
	if overrides != nil && overrides.workflowTaskHander != nil {
		taskHandler = overrides.workflowTaskHander
	}
	identity := params.Identity
	if identity == "" {
		identity = GetWorkerIdentity(params.TaskListName)
	}
	poller := newWorkflowTaskPoller(
		service,
		params.TaskListName,
		identity,
		taskHandler)
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
func NewActivityWorker(executionParameters WorkerExecutionParameters, service m.TChanWorkflowService) *ActivityWorker {
	return newActivityWorkerInternal(executionParameters, service, nil)
}

func newActivityWorkerInternal(executionParameters WorkerExecutionParameters, service m.TChanWorkflowService,
	overrides *workerOverrides) *ActivityWorker {
	var taskHandler ActivityTaskHandler // = &ActivityTaskHandler{}
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	}
	identity := executionParameters.Identity
	if identity == "" {
		identity = GetWorkerIdentity(executionParameters.TaskListName)
	}
	poller := newActivityTaskPoller(
		service,
		executionParameters.TaskListName,
		identity,
		taskHandler)
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

// AddActivityImplementationInstance adds an instance for the registry.
func (aw *ActivityWorker) AddActivityImplementationInstance(activityType m.ActivityType, activity ActivityImplementation) {
	aw.activityRegistry[activityType.GetName()] = activity
}

// Start the worker.
func (aw *ActivityWorker) Start() {
	// TODO: register all the types with activity event handler
	aw.worker.Start()
}

// Shutdown the worker.
func (aw *ActivityWorker) Shutdown() {
	aw.worker.Shutdown()
}
