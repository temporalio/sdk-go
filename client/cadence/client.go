package cadence

import (
	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/tally"
)

type (
	// Client is the client for starting and getting information about a workflow executions as well as
	// completing activities asynchronously.
	Client interface {
		// StartWorkflow starts a workflow execution
		// The user can use this to start using a function or workflow type name.
		// Either by
		//     StartWorkflow(options, "workflowTypeName", input)
		//     or
		//     StartWorkflow(options, workflowExecuteFn, arg1, arg2, arg3)
		StartWorkflow(options StartWorkflowOptions, workflow interface{}, args ...interface{}) (*WorkflowExecution, error)

		// GetWorkflowHistory gets history of a particular workflow.
		GetWorkflowHistory(workflowID string, runID string) (*s.History, error)

		// CompleteActivity reports activity completed.
		// activity Execute method can return cadence.ErrActivityResultPending to
		// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
		// should be called when that activity is completed with the actual result and error. If err is nil, activity task
		// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
		// activity task failed event will be reported.
		// An activity implementation should use GetActivityInfo(ctx).TaskToken function to get task token to use for completion.
		CompleteActivity(taskToken, result []byte, err error) error

		// RecordActivityHeartbeat records heartbeat for an activity.
		RecordActivityHeartbeat(taskToken, details []byte) error
	}

	// ClientOptions are optional parameters for Client creation.
	ClientOptions struct {
		MetricsScope tally.Scope
		Identity     string
	}

	// StartWorkflowOptions configuration parameters for starting a workflow execution.
	StartWorkflowOptions struct {
		ID                                     string
		TaskList                               string
		ExecutionStartToCloseTimeoutSeconds    int32
		DecisionTaskStartToCloseTimeoutSeconds int32
	}
)

// NewClient creates an instance of a workflow client
func NewClient(service m.TChanWorkflowService, domain string, options *ClientOptions) Client {
	var identity string
	if options == nil || options.Identity == "" {
		identity = getWorkerIdentity("")
	} else {
		identity = options.Identity
	}
	var metricScope tally.Scope
	if options != nil {
		metricScope = options.MetricsScope
	}
	return &workflowClient{
		workflowService: service,
		domain:          domain,
		metricsScope:    metricScope,
		identity:        identity,
	}
}
