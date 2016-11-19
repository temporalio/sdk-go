package flow

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
)

type (
	// ResultHandler that returns result
	ResultHandler func(err error, result []byte)

	// WorkflowContext Represents the context for workflow/decider.
	// Should only be used within the scope of workflow definition
	// TODO: Should model around GO context (When adding Cancel feature)
	WorkflowContext interface {
		ActivityClient
		WorkflowInfo() WorkflowInfo
		Complete(result []byte)
		Fail(err error)
	}

	// ActivityExecutionContext is context object passed to an activity implementation.
	// TODO: Should model around GO context (When adding Cancel feature)
	ActivityExecutionContext interface {
		GetTaskToken() string
		RecordActivityHeartbeat(details []byte)
	}

	// WorkflowDefinition wraps the code that can execute a workflow.
	WorkflowDefinition interface {
		Execute(context WorkflowContext, input []byte)
	}

	// WorkflowDefinitionFactory that returns a workflow definition for a specific
	// workflow type.
	WorkflowDefinitionFactory interface {
		GetWorkflowDefinition(workflowType m.WorkflowType) (WorkflowDefinition, error)
	}

	// ActivityImplementation wraps the code to execute an activity
	ActivityImplementation interface {
		Execute(context ActivityExecutionContext, input []byte) ([]byte, error)
	}

	// ExecuteActivityParameters configuration parameters for scheduling an activity
	ExecuteActivityParameters struct {
		ActivityID                    string
		ActivityType                  m.ActivityType
		TaskListName                  string
		Input                         []byte
		ScheduleToCloseTimeoutSeconds int
		ScheduleToStartTimeoutSeconds int
		StartToCloseTimeoutSeconds    int
		HeartbeatTimeoutSeconds       int
	}

	// ActivityClient for dynamically schedule an activity for execution
	ActivityClient interface {
		ScheduleActivityTask(parameters ExecuteActivityParameters, callback ResultHandler)
	}

	// StartWorkflowOptions configuration parameters for starting a workflow
	StartWorkflowOptions struct {
		WorkflowID                             string
		WorkflowType                           m.WorkflowType
		TaskListName                           string
		WorkflowInput                          []byte
		ExecutionStartToCloseTimeoutSeconds    int
		DecisionTaskStartToCloseTimeoutSeconds int
	}

	// WorkflowClient is the client facing for starting a workflow.
	WorkflowClient struct {
		options           StartWorkflowOptions
		workflowExecution m.WorkflowExecution

		// struct methods.
		// WorkflowExecution() m.WorkflowExecution
		// WorkflowType() m.WorkflowType
		// StartWorkflowExecution() (m.WorkflowExecution, error)
	}

	// WorkflowInfo is the information that the decider has access to during workflow execution.
	WorkflowInfo struct {
		workflowExecution m.WorkflowExecution
		workflowType      m.WorkflowType
		taskListName      string
	}
)

// NewWorkflowClient creates an instance of workflow client that users can start a workflow
func NewWorkflowClient(options StartWorkflowOptions) *WorkflowClient {
	return &WorkflowClient{options: options}
}

// StartWorkflowExecution starts a workflow execution
func (wc *WorkflowClient) StartWorkflowExecution() (m.WorkflowExecution, error) {
	// TODO:
	return wc.workflowExecution, nil
}
