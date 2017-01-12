package cadence

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"github.com/pborman/uuid"
	"github.com/uber-common/bark"
	"github.com/uber/tchannel-go/thrift"
)

type (
	// Lifecycle represents objects that can be started and stopped.
	// Both activity and workflow workers implement this interface.
	Lifecycle interface {
		Stop()
		Start() error
	}

	// WorkerExecutionParameters defines worker configure/execution options.
	WorkerExecutionParameters struct {
		// Task list name to poll.
		TaskList string

		// Defines how many concurrent poll requests for the task list by this worker.
		ConcurrentPollRoutineSize int

		// Defines how many executions for task list by this worker.
		// TODO: In future we want to separate the activity executions as they take longer than polls.
		// ConcurrentExecutionRoutineSize int

		// User can provide an identity for the debuggability. If not provided the framework has
		// a default option.
		Identity string
	}

	// WorkflowType identifies a workflow type.
	WorkflowType struct {
		Name string
	}

	// WorkflowExecution Details.
	WorkflowExecution struct {
		ID    string
		RunID string
	}

	// StartWorkflowOptions configuration parameters for starting a workflow
	StartWorkflowOptions struct {
		ID                                     string
		Type                                   WorkflowType
		TaskList                               string
		Input                                  []byte
		ExecutionStartToCloseTimeoutSeconds    int32
		DecisionTaskStartToCloseTimeoutSeconds int32
		Identity                               string
	}

	// WorkflowClient is the client facing for starting a workflow.
	WorkflowClient struct {
		workflowExecution WorkflowExecution
		workflowService   m.TChanWorkflowService
		reporter          metrics.Reporter
	}
)

// NewActivityWorker returns an instance of the activity worker.
func NewActivityWorker(executionParameters WorkerExecutionParameters, activities []Activity,
	service m.TChanWorkflowService, logger bark.Logger, reporter metrics.Reporter) (worker Lifecycle) {
	return newActivityWorkerInternal(executionParameters, activities, service, logger, reporter, nil)
}

// WorkflowFactory function is used to create a workflow implementation object.
// It is needed as a workflow objbect is created on every decision.
// To start a workflow instance use NewWorkflowClient(...).StartWorkflowExecution(...)
type WorkflowFactory func(workflowType WorkflowType) (Workflow, Error)

// NewWorkflowWorker returns an instance of a workflow worker.
func NewWorkflowWorker(
	params WorkerExecutionParameters,
	factory WorkflowFactory,
	service m.TChanWorkflowService,
	logger bark.Logger,
	reporter metrics.Reporter,
	pressurePoints map[string]map[string]string) (worker Lifecycle) {
	return newWorkflowWorker(
		params,
		func(workflowType WorkflowType) (workflowDefinition, Error) {
			wd, err := factory(workflowType)
			if err != nil {
				return nil, err
			}
			return NewWorkflowDefinition(wd), nil
		},
		service,
		logger,
		reporter,
		pressurePoints)
}

// NewWorkflowClient creates an instance of workflow client that users can start a workflow
func NewWorkflowClient(service m.TChanWorkflowService, reporter metrics.Reporter) *WorkflowClient {
	return &WorkflowClient{workflowService: service, reporter: reporter}
}

// StartWorkflowExecution starts a workflow execution
func (wc *WorkflowClient) StartWorkflowExecution(options StartWorkflowOptions) (*WorkflowExecution, error) {
	// Get an identity.
	identity := options.Identity
	if identity == "" {
		identity = getWorkerIdentity(options.TaskList)
	}
	workflowID := options.ID
	if workflowID == "" {
		workflowID = uuid.NewRandom().String()
	}

	startRequest := &s.StartWorkflowExecutionRequest{
		WorkflowId:   common.StringPtr(workflowID),
		WorkflowType: workflowTypePtr(options.Type),
		TaskList:     common.TaskListPtr(s.TaskList{Name: common.StringPtr(options.TaskList)}),
		Input:        options.Input,
		ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(options.ExecutionStartToCloseTimeoutSeconds),
		TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(options.DecisionTaskStartToCloseTimeoutSeconds),
		Identity:                            common.StringPtr(identity)}

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
		ID:    options.ID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}
