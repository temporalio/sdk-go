package internal

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type (
	// WorkerOptions is used to configure a worker instance.
	// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
	// subjected to change in the future.
	WorkerOptions struct {
		// Optional: To set the maximum concurrent activity executions this worker can have.
		// The zero value of this uses the default value.
		// default: defaultMaxConcurrentActivityExecutionSize(1k)
		MaxConcurrentActivityExecutionSize int

		// Optional: Sets the rate limiting on number of activities that can be executed per second per
		// worker. This can be used to limit resources used by the worker.
		// Notice that the number is represented in float, so that you can set it to less than
		// 1 if needed. For example, set the number to 0.1 means you want your activity to be executed
		// once for every 10 seconds. This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value
		// default: 100k
		WorkerActivitiesPerSecond float64

		// Optional: To set the maximum concurrent local activity executions this worker can have.
		// The zero value of this uses the default value.
		// default: 1k
		MaxConcurrentLocalActivityExecutionSize int

		// Optional: Sets the rate limiting on number of local activities that can be executed per second per
		// worker. This can be used to limit resources used by the worker.
		// Notice that the number is represented in float, so that you can set it to less than
		// 1 if needed. For example, set the number to 0.1 means you want your local activity to be executed
		// once for every 10 seconds. This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value
		// default: 100k
		WorkerLocalActivitiesPerSecond float64

		// Optional: Sets the rate limiting on number of activities that can be executed per second.
		// This is managed by the server and controls activities per second for your entire tasklist
		// whereas WorkerActivityTasksPerSecond controls activities only per worker.
		// Notice that the number is represented in float, so that you can set it to less than
		// 1 if needed. For example, set the number to 0.1 means you want your activity to be executed
		// once for every 10 seconds. This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value.
		// default: 100k
		TaskListActivitiesPerSecond float64

		// Optional: Sets the maximum number of goroutines that will concurrently poll the
		// temporal-server to retrieve activity tasks. Changing this value will affect the
		// rate at which the worker is able to consume tasks from a task list.
		// default: 2
		MaxConcurrentActivityTaskPollers int

		// Optional: To set the maximum concurrent decision task executions this worker can have.
		// The zero value of this uses the default value.
		// default: defaultMaxConcurrentTaskExecutionSize(1k)
		MaxConcurrentDecisionTaskExecutionSize int

		// Optional: Sets the rate limiting on number of decision tasks that can be executed per second per
		// worker. This can be used to limit resources used by the worker.
		// The zero value of this uses the default value.
		// default: 100k
		WorkerDecisionTasksPerSecond float64

		// Optional: Sets the maximum number of goroutines that will concurrently poll the
		// temporal-server to retrieve decision tasks. Changing this value will affect the
		// rate at which the worker is able to consume tasks from a task list.
		// default: 2
		MaxConcurrentDecisionTaskPollers int

		// Optional: Logger framework can use to log.
		// default: default logger provided.
		Logger *zap.Logger

		// Optional: Enable logging in replay.
		// In the workflow code you can use workflow.GetLogger(ctx) to write logs. By default, the logger will skip log
		// entry during replay mode so you won't see duplicate logs. This option will enable the logging in replay mode.
		// This is only useful for debugging purpose.
		// default: false
		EnableLoggingInReplay bool

		// Optional: Disable running workflow workers.
		// default: false
		DisableWorkflowWorker bool

		// Optional: Disable running activity workers.
		// default: false
		DisableActivityWorker bool

		// Optional: Disable sticky execution.
		// Sticky Execution is to run the decision tasks for one workflow execution on same worker host. This is an
		// optimization for workflow execution. When sticky execution is enabled, worker keeps the workflow state in
		// memory. New decision task contains the new history events will be dispatched to the same worker. If this
		// worker crashes, the sticky decision task will timeout after StickyScheduleToStartTimeout, and temporal server
		// will clear the stickiness for that workflow execution and automatically reschedule a new decision task that
		// is available for any worker to pick up and resume the progress.
		// default: false
		DisableStickyExecution bool

		// Optional: Sticky schedule to start timeout.
		// The resolution is seconds. See details about StickyExecution on the comments for DisableStickyExecution.
		// default: 5s
		StickyScheduleToStartTimeout time.Duration

		// Optional: sets context for activity. The context can be used to pass any configuration to activity
		// like common logger for all activities.
		BackgroundActivityContext context.Context

		// Optional: Sets how decision worker deals with non-deterministic history events
		// (presumably arising from non-deterministic workflow definitions or non-backward compatible workflow definition changes).
		// default: NonDeterministicWorkflowPolicyBlockWorkflow, which just logs error but reply nothing back to server
		NonDeterministicWorkflowPolicy NonDeterministicWorkflowPolicy

		// Optional: worker graceful shutdown timeout
		// default: 0s
		WorkerStopTimeout time.Duration

		// Optional: Enable running session workers.
		// Session workers is for activities within a session.
		// Enable this option to allow worker to process sessions.
		// default: false
		EnableSessionWorker bool

		// Uncomment this option when we support automatic restablish failed sessions.
		// Optional: The identifier of the resource consumed by sessions.
		// It's the user's responsibility to ensure there's only one worker using this resourceID.
		// For now, if user doesn't specify one, a new uuid will be used as the resourceID.
		// SessionResourceID string

		// Optional: Sets the maximum number of concurrently running sessions the resource support.
		// default: 1000
		MaxConcurrentSessionExecutionSize int

		// Optional: Specifies factories used to instantiate workflow interceptor chain
		// The chain is instantiated per each replay of a workflow execution
		WorkflowInterceptorChainFactories []WorkflowInterceptorFactory
	}
)

// NonDeterministicWorkflowPolicy is an enum for configuring how client's decision task handler deals with
// mismatched history events (presumably arising from non-deterministic workflow definitions).
type NonDeterministicWorkflowPolicy int

const (
	// NonDeterministicWorkflowPolicyBlockWorkflow is the default policy for handling detected non-determinism.
	// This option simply logs to console with an error message that non-determinism is detected, but
	// does *NOT* reply anything back to the server.
	// It is chosen as default for backward compatibility reasons because it preserves the old behavior
	// for handling non-determinism that we had before NonDeterministicWorkflowPolicy type was added to
	// allow more configurability.
	NonDeterministicWorkflowPolicyBlockWorkflow NonDeterministicWorkflowPolicy = iota
	// NonDeterministicWorkflowPolicyFailWorkflow behaves exactly the same as Ignore, up until the very
	// end of processing a decision task.
	// Whereas default does *NOT* reply anything back to the server, fail workflow replies back with a request
	// to fail the workflow execution.
	NonDeterministicWorkflowPolicyFailWorkflow

	// ReplayNamespace is namespace for replay because startEvent doesn't contain it
	ReplayNamespace = "ReplayNamespace"
)

// IsReplayNamespace checks if the namespace is from replay
func IsReplayNamespace(dn string) bool {
	return ReplayNamespace == dn
}

// NewWorker creates an instance of worker for managing workflow and activity executions.
// client   - client created with client.NewClient().
// taskList - is the task list name you use to identify your client worker, also
//            identifies group of workflow and activity implementations that are hosted by a single worker process.
// options 	- configure any worker specific options.
func NewWorker(
	client Client,
	taskList string,
	options WorkerOptions,
) *AggregatedWorker {
	// TODO: refactor and remove this downcast: https://github.com/temporalio/temporal-go-sdk/issues/70
	workflowClient, ok := client.(*WorkflowClient)
	if !ok {
		panic("Client must be created with client.NewClient()")
	}
	return NewAggregatedWorker(workflowClient, taskList, options)
}
