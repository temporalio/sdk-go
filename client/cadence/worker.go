package cadence

import (
	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/common"
	"github.com/uber-go/cadence-client/common/backoff"
	"github.com/uber-go/cadence-client/common/metrics"
	"errors"
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

		MetricsScope tally.Scope

		Logger bark.Logger
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
func NewActivityWorker(
	activities []Activity,
	service m.TChanWorkflowService,
	executionParameters WorkerExecutionParameters,
) (worker Lifecycle) {
	return newActivityWorkerInternal(activities, service, executionParameters, nil)
}

// WorkflowFactory function is used to create a workflow implementation object.
// It is needed as a workflow objbect is created on every decision.
// To start a workflow instance use NewWorkflowClient(...).StartWorkflowExecution(...)
type WorkflowFactory func(workflowType WorkflowType) (Workflow, error)

// NewWorkflowWorker returns an instance of a workflow worker.
func NewWorkflowWorker(
	factory WorkflowFactory,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) (worker Lifecycle) {
	return newWorkflowWorker(
		getWorkflowDefinitionFactory(factory),
		service,
		params,
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
		RequestId:    common.StringPtr(uuid.New()),
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
	workflowDefFactory workflowDefinitionFactory
	logger             bark.Logger
	task               *s.PollForDecisionTaskResponse
	decisions          *s.RespondDecisionTaskCompletedRequest
	stackTrace         string
}

// NewWorkflowReplayer creates an instance of WorkflowReplayer
func NewWorkflowReplayer(o WorkflowReplayerOptions, logger bark.Logger) *WorkflowReplayer {
	workflowTask := &s.PollForDecisionTaskResponse{
		TaskToken: []byte("replayer-token"),
		History:   o.History,
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(o.Execution.ID),
			RunId:      common.StringPtr(o.Execution.RunID),
		},
		WorkflowType: &s.WorkflowType{Name: common.StringPtr(o.Type.Name)},
	}
	return NewWorkflowReplayerForPoll(workflowTask, o.Factory, logger)
}

// NewWorkflowReplayerForPoll creates an instance of WorkflowReplayer from decision poll response
func NewWorkflowReplayerForPoll(task *s.PollForDecisionTaskResponse, factory WorkflowFactory, logger bark.Logger) *WorkflowReplayer {
	return &WorkflowReplayer{
		workflowDefFactory: getWorkflowDefinitionFactory(factory),
		logger:             logger,
		task:               task,
	}
}

// Process replays the history.
func (wr *WorkflowReplayer) Process(emitStack bool) (err error) {
	history := wr.task.GetHistory()
	if history == nil {
		return errors.New("nil history")
	}
	event := history.Events[0]
	if history == nil {
		return errors.New("nil first history event")
	}
	attributes := event.GetWorkflowExecutionStartedEventAttributes()
	if attributes == nil {
		return errors.New("first history event is not WorkflowExecutionStarted")
	}
	taskList := attributes.GetTaskList()
	if taskList == nil {
		return errors.New("nil taskList in WorkflowExecutionStarted event")
	}
	params := WorkerExecutionParameters{
		TaskList: taskList.GetName(),
		Identity: getWorkerIdentity(taskList.GetName()),
		Logger:   wr.logger,
	}
	taskHandler := newWorkflowTaskHandler(
		wr.workflowDefFactory,
		params,
		nil)
	wr.decisions, wr.stackTrace, err = taskHandler.ProcessWorkflowTask(wr.task, emitStack)
	return err
}

// StackTrace returns the stack trace dump of all current workflow goroutines
func (wr *WorkflowReplayer) StackTrace() string {
	return wr.stackTrace
}

// Decisions that are result of a decision task.
func (wr *WorkflowReplayer) Decisions() *s.RespondDecisionTaskCompletedRequest {
	return wr.decisions
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
