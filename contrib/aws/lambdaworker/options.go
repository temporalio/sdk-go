package lambdaworker

import (
	"context"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Compile-time check that ConfigureWorkerContext implements worker.Registry.
var _ worker.Registry = (*Options)(nil)

// Options is passed to the configure callback of [RunWorker]. It implements [worker.Registry] so
// that workflows, activities, and Nexus services can be registered directly on it.
//
// Public fields are pre-populated with Lambda-appropriate defaults before the configure callback
// is invoked; the callback may override any of them.
type Options struct {
	// TaskQueue is the task queue name for the worker. Pre-populated from the
	// TEMPORAL_TASK_QUEUE environment variable if set; otherwise must be set by the callback.
	TaskQueue string

	// ClientOptions are the Temporal client options used to dial the server. Pre-populated
	// from the config file / environment variables via envconfig, with Lambda defaults applied.
	ClientOptions client.Options

	// WorkerOptions are the Temporal worker options. Pre-populated with Lambda-appropriate
	// defaults (low concurrency, eager activities disabled).
	WorkerOptions worker.Options

	// ShutdownDeadlineBuffer is how long before the Lambda invocation deadline the worker
	// begins its shutdown sequence (worker drain + shutdown hooks). Pre-populated to
	// WorkerOptions.WorkerStopTimeout + 2s. If you change WorkerStopTimeout, adjust this
	// accordingly. The buffer must be large enough to accommodate both the worker stop timeout
	// and any shutdown hooks (e.g. telemetry flushes).
	ShutdownDeadlineBuffer time.Duration

	registrations []func(worker.Registry)
	shutdownFuncs []func(context.Context) error
}

// RegisterWorkflow registers a workflow on the worker. See
// [worker.WorkflowRegistry.RegisterWorkflow] for details.
func (c *Options) RegisterWorkflow(w interface{}) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterWorkflow(w) })
}

// RegisterWorkflowWithOptions registers a workflow with options on the worker. See
// [worker.WorkflowRegistry.RegisterWorkflowWithOptions] for details.
func (c *Options) RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterWorkflowWithOptions(w, options) })
}

// RegisterDynamicWorkflow registers a dynamic workflow on the worker. See
// [worker.WorkflowRegistry.RegisterDynamicWorkflow] for details.
func (c *Options) RegisterDynamicWorkflow(w interface{}, options workflow.DynamicRegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterDynamicWorkflow(w, options) })
}

// RegisterActivity registers an activity on the worker. See
// [worker.ActivityRegistry.RegisterActivity] for details.
func (c *Options) RegisterActivity(a interface{}) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterActivity(a) })
}

// RegisterActivityWithOptions registers an activity with options on the worker. See
// [worker.ActivityRegistry.RegisterActivityWithOptions] for details.
func (c *Options) RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterActivityWithOptions(a, options) })
}

// RegisterDynamicActivity registers a dynamic activity on the worker. See
// [worker.ActivityRegistry.RegisterDynamicActivity] for details.
func (c *Options) RegisterDynamicActivity(a interface{}, options activity.DynamicRegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterDynamicActivity(a, options) })
}

// RegisterNexusService registers a Nexus service on the worker. See
// [worker.NexusServiceRegistry.RegisterNexusService] for details.
func (c *Options) RegisterNexusService(s *nexus.Service) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterNexusService(s) })
}

// OnShutdown registers a function to be called at the end of each Lambda invocation, after the
// worker has stopped. Shutdown functions run in registration order and receive a background context
// with no deadline — Lambda hard-kills the process at the invocation deadline regardless. Use this
// to flush telemetry providers or release other per-process resources.
func (c *Options) OnShutdown(fn func(context.Context) error) {
	c.shutdownFuncs = append(c.shutdownFuncs, fn)
}

// replayRegistrations replays all buffered registrations onto the given worker.
func (c *Options) replayRegistrations(w worker.Worker) {
	for _, fn := range c.registrations {
		fn(w)
	}
}
