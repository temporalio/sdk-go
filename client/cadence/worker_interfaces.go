package cadence

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
)

type (

	// WorkflowType identifies a workflow type.
	WorkflowType struct {
		Name string
	}

	// ActivityType identifies a activity type.
	ActivityType struct {
		Name string
	}

	// WorkflowExecution Details.
	WorkflowExecution struct {
		WorkflowID string
		RunID      string
	}

	// resultHandler that returns result
	resultHandler func(result []byte, err Error)

	// workflowEnvironment Represents the environment for workflow/decider.
	// Should only be used within the scope of workflow definition
	workflowEnvironment interface {
		asyncActivityClient
		WorkflowInfo() *WorkflowInfo
		Complete(result []byte, err Error)
	}

	// ActivityExecutionContext is context object passed to an activity implementation.
	// TODO: Should model around GO context (When adding Cancel feature)
	ActivityExecutionContext interface {
		TaskToken() []byte
		RecordActivityHeartbeat(details []byte) error
	}

	// WorkflowDefinition wraps the code that can execute a workflow.
	WorkflowDefinition interface {
		Execute(env workflowEnvironment, input []byte)
		StackTrace() string // Stack trace of all coroutines owned by the Dispatcher instance
	}

	// ActivityImplementation wraps the code to execute an activity
	ActivityImplementation interface {
		Execute(context ActivityExecutionContext, input []byte) ([]byte, Error)
	}

	// WorkflowDefinitionFactory that returns a workflow definition for a specific
	// workflow type.
	WorkflowDefinitionFactory func(workflowType WorkflowType) (WorkflowDefinition, Error)

	// ActivityImplementationFactory that returns a activity implementation for a specific
	// activity type.
	ActivityImplementationFactory func(activityType ActivityType) (ActivityImplementation, Error)

	// ExecuteActivityParameters configuration parameters for scheduling an activity
	ExecuteActivityParameters struct {
		ActivityID                    *string // Users can choose IDs but our framework makes it optional to decrease the crust.
		ActivityType                  ActivityType
		TaskListName                  string
		Input                         []byte
		ScheduleToCloseTimeoutSeconds int32
		ScheduleToStartTimeoutSeconds int32
		StartToCloseTimeoutSeconds    int32
		HeartbeatTimeoutSeconds       int32
	}

	// asyncActivityClient for requesting activity execution
	asyncActivityClient interface {
		ExecuteActivity(parameters ExecuteActivityParameters, callback resultHandler)
	}

	// StartWorkflowOptions configuration parameters for starting a workflow
	StartWorkflowOptions struct {
		WorkflowID                             string
		WorkflowType                           WorkflowType
		TaskListName                           string
		WorkflowInput                          []byte
		ExecutionStartToCloseTimeoutSeconds    int32
		DecisionTaskStartToCloseTimeoutSeconds int32
		Identity                               string
	}

	// WorkflowClient is the client facing for starting a workflow.
	WorkflowClient struct {
		options           StartWorkflowOptions
		workflowExecution WorkflowExecution
		workflowService   m.TChanWorkflowService
		Identity          string
		reporter          metrics.Reporter
	}

	// WorkflowInfo is the information that the decider has access to during workflow execution.
	WorkflowInfo struct {
		workflowExecution WorkflowExecution
		workflowType      WorkflowType
		taskListName      string
	}
)
