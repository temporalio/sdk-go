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
		ConcurrentPollingSize int
		// Defines how many task executor for the task list by this worker.
		TaskExecutorPoolSize int
	}

	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	WorkflowWorker struct {
		executionParameters WorkerExecutionParameters
		workflowDefFactory  WorkflowDefinitionFactory
		workflowService     m.TChanWorkflowService
		// TaskPoller to poll the tasks.
		pollerTask TaskPoller
	}

	// ActivityWorker wraps the code for hosting activity types.
	ActivityWorker struct {
		executionParameters WorkerExecutionParameters
		activityRegistry    map[m.ActivityType]*ActivityImplementation
		workflowService     m.TChanWorkflowService
		// TaskPoller to poll the tasks.
		pollerTask TaskPoller
	}
)

// NewWorkflowWorker returns an instance of the workflow worker.
func NewWorkflowWorker(params WorkerExecutionParameters, factory WorkflowDefinitionFactory, service m.TChanWorkflowService) *WorkflowWorker {
	return &WorkflowWorker{
		executionParameters: params,
		workflowDefFactory:  factory,
		workflowService:     service,
		// PollerTask: &DecisionTaskPoller{}
	}
}

// Start the worker.
func (ww *WorkflowWorker) Start() error {
	// TODO:
	return nil
}

// Stop the worker.
func (ww *WorkflowWorker) Stop() error {
	// TODO:
	return nil
}

// NewActivityWorker returns an instance of the activity worker.
func NewActivityWorker(executionParameters WorkerExecutionParameters, service m.TChanWorkflowService) *ActivityWorker {
	return &ActivityWorker{
		executionParameters: executionParameters,
		activityRegistry:    make(map[m.ActivityType]*ActivityImplementation),
		workflowService:     service,
		// PollerTask: &ActivityTaskPoller{}
	}
}

// AddActivityImplementationInstance adds an instance for the registry.
func (aw *ActivityWorker) AddActivityImplementationInstance(activity ActivityImplementation) error {
	// TODO:
	return nil
}

// Start the worker.
func (aw *ActivityWorker) Start() error {
	// TODO:
	return nil
}

// Stop the worker.
func (aw *ActivityWorker) Stop() error {
	// TODO:
	return nil
}
