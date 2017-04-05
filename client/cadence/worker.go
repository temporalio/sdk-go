package cadence

import (
	"errors"

	"github.com/pborman/uuid"
	"github.com/uber-common/bark"
	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/common"
	"github.com/uber-go/cadence-client/common/backoff"
	"github.com/uber-go/cadence-client/common/metrics"
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
		TaskList                               string
		ExecutionStartToCloseTimeoutSeconds    int32
		DecisionTaskStartToCloseTimeoutSeconds int32
		Identity                               string
	}

	// WorkflowClient is the client facing for starting a workflow.
	WorkflowClient struct {
		workflowExecution WorkflowExecution
		workflowService   m.TChanWorkflowService
		metricsScope      tally.Scope
		identity          string
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
func NewWorkflowClient(service m.TChanWorkflowService, metricsScope tally.Scope, identity string) *WorkflowClient {
	if identity == "" {
		identity = getWorkerIdentity("")
	}
	return &WorkflowClient{workflowService: service, metricsScope: metricsScope, identity: identity}
}

// StartWorkflowExecution starts a workflow execution
// The user can use this to start using a functor like.
// Either by
//     StartWorkflowExecution(options, "workflowTypeName", input)
//     or
//     StartWorkflowExecution(options, workflowExecuteFn, arg1, arg2, arg3)
func (wc *WorkflowClient) StartWorkflowExecution(
	options StartWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) (*WorkflowExecution, error) {
	// Get an identity.
	identity := options.Identity
	if identity == "" {
		identity = getWorkerIdentity(options.TaskList)
	}
	workflowID := options.ID
	if workflowID == "" {
		workflowID = uuid.NewRandom().String()
	}

	// Validate type and its arguments.
	workflowType, input, err := getValidatedWorkerFunction(workflowFunc, args)
	if err != nil {
		return nil, err
	}

	startRequest := &s.StartWorkflowExecutionRequest{
		RequestId:    common.StringPtr(uuid.New()),
		WorkflowId:   common.StringPtr(workflowID),
		WorkflowType: workflowTypePtr(*workflowType),
		TaskList:     common.TaskListPtr(s.TaskList{Name: common.StringPtr(options.TaskList)}),
		Input:        input,
		ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(options.ExecutionStartToCloseTimeoutSeconds),
		TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(options.DecisionTaskStartToCloseTimeoutSeconds),
		Identity:                            common.StringPtr(identity)}

	var response *s.StartWorkflowExecutionResponse

	// Start creating workflow request.
	err = backoff.Retry(
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

// CompleteActivity reports activity completed. Activity Execute method can return cadence.ActivityResultPendingError to
// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
// should be called when that activity is completed with the actual result and error. If err is nil, activity task
// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
// activity task failed event will be reported.
func (wc *WorkflowClient) CompleteActivity(taskToken, result []byte, err error) error {
	request := convertActivityResultToRespondRequest(wc.identity, taskToken, result, err)
	return reportActivityComplete(wc.workflowService, request)
}

// RecordActivityHeartbeat records heartbeat for an activity.
func (wc *WorkflowClient) RecordActivityHeartbeat(taskToken, details []byte) error {
	return recordActivityHeartbeat(wc.workflowService, wc.identity, taskToken, details)
}

// WorkflowReplayerOptions represents options for workflow replayer.
type WorkflowReplayerOptions struct {
	Execution WorkflowExecution
	History   *s.History
	Logger bark.Logger
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
func NewWorkflowReplayer(
	options WorkflowReplayerOptions,
	workferFunc interface{},
) *WorkflowReplayer {
	fnName := getFunctionName(workferFunc)
	workflowFactory := func(wt WorkflowType) (Workflow, error) {
		return &workflowExecutor{name: fnName, fn: workferFunc}, nil
	}
	workflowTask := &s.PollForDecisionTaskResponse{
		TaskToken: []byte("replayer-token"),
		History:   options.History,
		WorkflowExecution: &s.WorkflowExecution{
			WorkflowId: common.StringPtr(options.Execution.ID),
			RunId:      common.StringPtr(options.Execution.RunID),
		},
		WorkflowType: &s.WorkflowType{Name: common.StringPtr(fnName)},
	}
	return NewWorkflowReplayerForPoll(workflowTask, workflowFactory, options.Logger)
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

// WorkerOptions is to configure a worker instance,
// for example (1) the logger or any specific metrics.
// 	       (2) Whether to heart beat for activities automatically.
type WorkerOptions interface {
	// Optional: To set the maximum concurrent activity executions this host can have.
	// default: defaultMaxConcurrentActivityExecutionSize(10k)
	SetMaxConcurrentActivityExecutionSize(size int) WorkerOptions

	// Optional: Sets the rate limiting on number of activities that can be executed.
	// This can be used to protect down stream services from flooding.
	// default: defaultMaxActivityExecutionRate(100k)
	SetMaxActivityExecutionRate(requestPerSecond float32) WorkerOptions

	// Optional: if the activities need auto heart beating for those activities
	// by the framework
	// default: false not to heartbeat.
	SetAutoHeartBeat(auto bool) WorkerOptions

	// Optional: Sets an identify that can be used to track this host for debugging.
	// default: default identity that include hostname, groupName and process ID.
	SetIdentity(identity string) WorkerOptions

	// Optional: Metrics to be reported.
	// default: no metrics.
	SetMetrics(metricsScope tally.Scope) WorkerOptions

	// Optional: Logger framework can use to log.
	// default: default logger provided.
	SetLogger(logger bark.Logger) WorkerOptions

	// Optional: Disable running workflow workers.
	// default: false
	SetDisableWorkflowWorker(disable bool) WorkerOptions

	// Optional: Disable running activity workers.
	// default: false
	SetDisableActivityWorker(disable bool) WorkerOptions
}

// NewWorkerOptions returns an instance of worker options to configure.
func NewWorkerOptions() WorkerOptions {
	return NewWorkerOptionsInternal(nil)
}

// RegisterWorkflow - registers a workflow function with the framework.
// A workflow takes a cadence context and input and returns a (result, error) or just error.
// Examples:
//	func sampleWorkflow(ctx cadence.Context, input []byte) (result []byte, err error)
//	func sampleWorkflow(ctx cadence.Context, arg1 int, arg2 string) (result []byte, err error)
//	func sampleWorkflow(ctx cadence.Context) (result []byte, err error)
//	func sampleWorkflow(ctx cadence.Context, arg1 int) (result string, err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
func RegisterWorkflow(
	workflowFunc interface{},
) error {
	thImpl := getHostEnvironment()
	return thImpl.RegisterWorkflow(workflowFunc)
}

// RegisterActivity - register a activity function with the framework.
// A activity takes a context and input and returns a (result, error) or just error.
// Examples:
//	func sampleActivity(ctx context.Context, input []byte) (result []byte, err error)
//	func sampleActivity(ctx context.Context, arg1 int, arg2 string) (result *customerStruct, err error)
//	func sampleActivity(ctx context.Context) (err error)
//	func sampleActivity() (result string, err error)
//	func sampleActivity(arg1 bool) (result int, err error)
//	func sampleActivity(arg1 bool) (err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
func RegisterActivity(
	activityFunc interface{},
) error {
	thImpl := getHostEnvironment()
	return thImpl.RegisterActivity(activityFunc)
}

// NewWorker creates an instance of worker for managing workflow and activity executions.
// service 	- thrift connection to the cadence server.
// groupName 	- is the name you use to identify your client worker, also
// 		  identifies group of workflow and activity implementations that are hosted by a single worker process.
// options 	-  configure any worker specific options like logger, metrics, identity.
func NewWorker(
	service m.TChanWorkflowService,
	groupName string,
	options WorkerOptions,
) Lifecycle {
	return newAggregatedWorker(service, groupName, options)
}
