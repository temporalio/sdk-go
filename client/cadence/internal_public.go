package cadence

// WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!
// Any of the APIs in this file are not supported for application level developers
// and are subject to change without any notice.
//
// APIs that are internal to Cadence system developers and are public from the Go
// point of view only to access them from other packages.

import (
	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"go.uber.org/zap"
)

type (
	// WorkflowTaskHandler represents decision task handlers.
	WorkflowTaskHandler interface {
		// Process the workflow task
		ProcessWorkflowTask(task *s.PollForDecisionTaskResponse, emitStack bool) (response *s.RespondDecisionTaskCompletedRequest, stackTrace string, err error)
	}

	// ActivityTaskHandler represents activity task handlers.
	ActivityTaskHandler interface {
		// Execute the activity task
		// The return interface{} can have three requests, use switch to find the type of it.
		// - RespondActivityTaskCompletedRequest
		// - RespondActivityTaskFailedRequest
		// - RespondActivityTaskCancelRequest
		Execute(task *s.PollForActivityTaskResponse) (interface{}, error)
	}
)

var enableVerboseLogging bool

// NewWorkerOptionsInternal creates an instance of worker options with default values.
func NewWorkerOptionsInternal(testTags map[string]map[string]string) WorkerOptions {
	return &workerOptions{
		maxConcurrentActivityExecutionSize: defaultMaxConcurrentActivityExecutionSize,
		maxActivityExecutionRate:           defaultMaxActivityExecutionRate,
		autoHeartBeatForActivities:         false,
		testTags:                           testTags,
		// Defaults for metrics, identity, logger is filled in by the WorkflowWorker APIs.
	}
}

// NewWorkflowTaskWorker returns an instance of a workflow task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewWorkflowTaskWorker(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	taskList string,
	options WorkerOptions,
) (worker Worker) {
	wOptions := options.(*workerOptions)
	workerParams := workerExecutionParameters{
		TaskList:                  taskList,
		ConcurrentPollRoutineSize: defaultConcurrentPollRoutineSize,
		Identity:                  wOptions.identity,
		MetricsScope:              wOptions.metricsScope,
		Logger:                    wOptions.logger,
	}

	processTestTags(wOptions, &workerParams)
	return newWorkflowTaskWorkerInternal(taskHandler, service, domain, workerParams)
}

// NewActivityTaskWorker returns instance of an activity task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewActivityTaskWorker(
	taskHandler ActivityTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	taskList string,
	options WorkerOptions,
) Worker {
	wOptions := options.(*workerOptions)
	workerParams := workerExecutionParameters{
		TaskList:                  taskList,
		ConcurrentPollRoutineSize: defaultConcurrentPollRoutineSize,
		Identity:                  wOptions.identity,
		MetricsScope:              wOptions.metricsScope,
		Logger:                    wOptions.logger,
	}

	processTestTags(wOptions, &workerParams)
	return newActivityTaskWorker(taskHandler, service, domain, workerParams)
}

// NewWorkflowTaskHandler creates an instance of a WorkflowTaskHandler from a decision poll response
// using workflow functions registered through RegisterWorkflow
// To be used to replay a workflow in a debugger.
func NewWorkflowTaskHandler(identity string, logger *zap.Logger) WorkflowTaskHandler {
	params := workerExecutionParameters{
		Identity: identity,
		Logger:   logger,
	}
	return newWorkflowTaskHandler(
		getWorkflowDefinitionFactory(newRegisteredWorkflowFactory()),
		params,
		nil)
}

// NewActivityTaskHandler creates an instance of a WorkflowTaskHandler from a decision poll response
// using activity functions registered through RegisterActivity. service parameter is used for
// heartbeating from activity implementation.
// To be used to invoke registered functions for debugging purposes.
func NewActivityTaskHandler(service m.TChanWorkflowService, identity string, logger *zap.Logger) ActivityTaskHandler {
	params := workerExecutionParameters{
		Identity: identity,
		Logger:   logger,
	}
	return newActivityTaskHandler(
		getHostEnvironment().getRegisteredActivities(),
		service,
		params)
}

// AddWorkflowRegistrationInterceptor adds interceptor that is called for each RegisterWorkflow call.
// This function guarantees that the interceptor function is called for each registration even
// if it itself is called from init()
func AddWorkflowRegistrationInterceptor(
	i func(name string, workflow interface{}) (string, interface{}),
) {
	getHostEnvironment().AddWorkflowRegistrationInterceptor(i)
}

// AddActivityRegistrationInterceptor adds interceptor that is called for each RegisterActivity call.
// This function guarantees that the interceptor function is called for each registration even
// if it itself is called from init()
func AddActivityRegistrationInterceptor(
	i func(name string, activity interface{}) (string, interface{})) {
	getHostEnvironment().AddActivityRegistrationInterceptor(i)
}

// SerializeFnArgs serializes an activity function arguments.
func SerializeFnArgs(args ...interface{}) ([]byte, error) {
	return getHostEnvironment().encodeArgs(args)
}

// DeserializeFnResults de-serializes a function results.
// The input result doesn't include the error. The cadence server has result, error.
// This is to de-serialize the result.
func DeserializeFnResults(result []byte, to interface{}) error {
	return getHostEnvironment().decodeArg(result, to)
}

// EnableVerboseLogging enable or disable verbose logging. This is for internal use only.
func EnableVerboseLogging(enable bool) {
	enableVerboseLogging = enable
}

// newDecodeFuture creates a new future as well as associated Settable that is used to set its value.
// fn - the decoded value needs to be validated against a function.
func newDecodeFuture(ctx Context, fn interface{}) (Future, Settable) {
	impl := &decodeFutureImpl{
		futureImpl{channel: NewChannel(ctx).(*channelImpl)}, fn}
	return impl, impl
}
