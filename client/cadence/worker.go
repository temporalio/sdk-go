package cadence

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
	"code.uber.internal/devexp/minions-client-go.git/common/backoff"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"github.com/pborman/uuid"
	"github.com/uber-common/bark"
	"github.com/uber-go/tally"
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
		metricsScope      tally.Scope
	}
)

// NewActivityWorker returns an instance of the activity worker.
func NewActivityWorker(executionParameters WorkerExecutionParameters, activities []Activity,
	service m.TChanWorkflowService, logger bark.Logger, metricsScope tally.Scope) (worker Lifecycle) {
	return newActivityWorkerInternal(executionParameters, activities, service, logger, metricsScope, nil)
}

// WorkflowFactory function is used to create a workflow implementation object.
// It is needed as a workflow objbect is created on every decision.
// To start a workflow instance use NewWorkflowClient(...).StartWorkflowExecution(...)
type WorkflowFactory func(workflowType WorkflowType) (Workflow, error)

// NewWorkflowWorker returns an instance of a workflow worker.
func NewWorkflowWorker(
	params WorkerExecutionParameters,
	factory WorkflowFactory,
	service m.TChanWorkflowService,
	logger bark.Logger,
	metricsScope tally.Scope) (worker Lifecycle) {
	return newWorkflowWorker(
		params,
		getWorkflowDefinitionFactory(factory),
		service,
		logger,
		metricsScope,
		nil)
}

// NewWorkflowClient creates an instance of workflow client that users can start a workflow
func NewWorkflowClient(service m.TChanWorkflowService, metricsScope tally.Scope) *WorkflowClient {
	return &WorkflowClient{workflowService: service, metricsScope: metricsScope}
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
			ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
			defer cancel()

			var err1 error
			response, err1 = wc.workflowService.StartWorkflowExecution(ctx, startRequest)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)

	if err != nil {
		return nil, err
	}

	if wc.metricsScope != nil {
		wc.metricsScope.Counter(metrics.WorkflowsStartTotalCounter).Inc(1)
	}

	executionInfo := &WorkflowExecution{
		ID:    options.ID,
		RunID: response.GetRunId()}
	return executionInfo, nil
}

// GetHistory gets history of a particular workflow.
func (wc *WorkflowClient) GetHistory(workflowID string, runID string) (*s.History, error) {
	request := &s.GetWorkflowExecutionHistoryRequest{
		Execution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      common.StringPtr(runID),
		},
	}

	var response *s.GetWorkflowExecutionHistoryResponse
	err := backoff.Retry(
		func() error {
			var err1 error
			ctx, cancel := common.NewTChannelContext(respondTaskServiceTimeOut, common.RetryDefaultOptions)
			defer cancel()
			response, err1 = wc.workflowService.GetWorkflowExecutionHistory(ctx, request)
			return err1
		}, serviceOperationRetryPolicy, isServiceTransientError)
	return response.GetHistory(), err
}

// WorkflowReplayerOptions represents options for workflow replayer.
type WorkflowReplayerOptions struct {
	Execution WorkflowExecution
	Type      WorkflowType
	Factory   WorkflowFactory
	History   *s.History
}

// WorkflowReplayer replays a given state of workflow execution.
type WorkflowReplayer struct {
	workflowExecution  WorkflowExecution
	workflowDefFactory workflowDefinitionFactory
	workflowType       WorkflowType
	history            *s.History
	logger             bark.Logger
	stackTrace         string
}

// NewWorkflowReplayer creates an isntance of WorkflowReplayer
func NewWorkflowReplayer(wfOptions WorkflowReplayerOptions, logger bark.Logger) *WorkflowReplayer {
	return &WorkflowReplayer{
		workflowExecution:  wfOptions.Execution,
		history:            wfOptions.History,
		workflowType:       wfOptions.Type,
		workflowDefFactory: getWorkflowDefinitionFactory(wfOptions.Factory),
		logger:             logger,
	}
}

// Replay replays the history.
func (wr *WorkflowReplayer) Replay() (err error) {
	workflowTask := &workflowTask{
		task: &s.PollForDecisionTaskResponse{
			TaskToken:         []byte("replayer-token"),
			History:           wr.history,
			WorkflowExecution: workflowExecutionPtr(wr.workflowExecution),
			WorkflowType:      workflowTypePtr(wr.workflowType),
		}}

	taskListName := "replayerTaskList"
	taskHandler := newWorkflowTaskHandler(taskListName, getWorkerIdentity(taskListName), wr.workflowDefFactory, wr.logger, nil, nil)
	_, wr.stackTrace, err = taskHandler.ProcessWorkflowTask(workflowTask, true /* emitStack */)
	return err
}

// StackTrace returns the current stack trace.
func (wr *WorkflowReplayer) StackTrace() string {
	return wr.stackTrace
}

func getWorkflowDefinitionFactory(factory WorkflowFactory) workflowDefinitionFactory {
	return func(workflowType WorkflowType) (workflowDefinition, error) {
		wd, err := factory(workflowType)
		if err != nil {
			return nil, err
		}
		return NewWorkflowDefinition(wd), nil
	}
}
