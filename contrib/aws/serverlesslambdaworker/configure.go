package serverlesslambdaworker

import (
	"context"
	"fmt"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Compile-time check that ConfigureWorkerContext implements worker.Registry.
var _ worker.Registry = (*ConfigureWorkerContext)(nil)

// ConfigureWorkerContext is passed to the configure callback of [RunWorker]. It implements
// [worker.Registry] so that workflows, activities, and Nexus services can be registered directly on
// it.
type ConfigureWorkerContext struct {
	taskQueue              string
	mutateClientOpts       func(*client.Options) error
	mutateWorkerOpts       func(*worker.Options) error
	registrations          []func(worker.Registry)
	shutdownFuncs          []func(context.Context) error
	shutdownDeadlineBuffer time.Duration
}

// RegisterWorkflow registers a workflow on the worker. See
// [worker.WorkflowRegistry.RegisterWorkflow] for details.
func (c *ConfigureWorkerContext) RegisterWorkflow(w interface{}) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterWorkflow(w) })
}

// RegisterWorkflowWithOptions registers a workflow with options on the worker. See
// [worker.WorkflowRegistry.RegisterWorkflowWithOptions] for details.
func (c *ConfigureWorkerContext) RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterWorkflowWithOptions(w, options) })
}

// RegisterDynamicWorkflow registers a dynamic workflow on the worker. See
// [worker.WorkflowRegistry.RegisterDynamicWorkflow] for details.
func (c *ConfigureWorkerContext) RegisterDynamicWorkflow(w interface{}, options workflow.DynamicRegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterDynamicWorkflow(w, options) })
}

// RegisterActivity registers an activity on the worker. See
// [worker.ActivityRegistry.RegisterActivity] for details.
func (c *ConfigureWorkerContext) RegisterActivity(a interface{}) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterActivity(a) })
}

// RegisterActivityWithOptions registers an activity with options on the worker. See
// [worker.ActivityRegistry.RegisterActivityWithOptions] for details.
func (c *ConfigureWorkerContext) RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterActivityWithOptions(a, options) })
}

// RegisterDynamicActivity registers a dynamic activity on the worker. See
// [worker.ActivityRegistry.RegisterDynamicActivity] for details.
func (c *ConfigureWorkerContext) RegisterDynamicActivity(a interface{}, options activity.DynamicRegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterDynamicActivity(a, options) })
}

// RegisterNexusService registers a Nexus service on the worker. See
// [worker.NexusServiceRegistry.RegisterNexusService] for details.
func (c *ConfigureWorkerContext) RegisterNexusService(s *nexus.Service) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterNexusService(s) })
}

// MutateClientOptions sets a callback that mutates client options before [client.Dial] is called.
// The callback receives options that already have Lambda defaults applied, so any values the
// callback sets will override Lambda defaults.
func (c *ConfigureWorkerContext) MutateClientOptions(fn func(*client.Options) error) {
	c.mutateClientOpts = fn
}

// MutateWorkerOptions sets a callback that mutates worker options before [worker.New] is called.
// The callback receives options that already have Lambda defaults applied, so any values the
// callback sets will override Lambda defaults.
func (c *ConfigureWorkerContext) MutateWorkerOptions(fn func(*worker.Options) error) {
	c.mutateWorkerOpts = fn
}

// OnShutdown registers a function to be called at the end of each Lambda invocation, after the
// worker has stopped. Shutdown functions run in registration order and receive a background context
// with no deadline — Lambda hard-kills the process at the invocation deadline regardless. Use this
// to flush telemetry providers or release other per-process resources.
func (c *ConfigureWorkerContext) OnShutdown(fn func(context.Context) error) {
	c.shutdownFuncs = append(c.shutdownFuncs, fn)
}

// SetShutdownDeadlineBuffer sets how long before the Lambda invocation deadline the worker begins
// its shutdown sequence (worker drain + shutdown hooks). If not set, it defaults to the
// WorkerStopTimeout plus 2 seconds for shutdown hooks.
//
// This value is independent of [worker.Options.WorkerStopTimeout], which controls how long
// [worker.Worker.Stop] waits for in-flight tasks to complete. The deadline buffer must be large
// enough to accommodate both the worker stop timeout and any shutdown hooks (e.g. telemetry
// flushes).
func (c *ConfigureWorkerContext) SetShutdownDeadlineBuffer(d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("shutdown deadline buffer must not be negative, got %v", d)
	}
	c.shutdownDeadlineBuffer = d
	return nil
}

// SetTaskQueue sets the task queue name for the worker. If not set, the TEMPORAL_TASK_QUEUE
// environment variable is used.
func (c *ConfigureWorkerContext) SetTaskQueue(taskQueue string) {
	c.taskQueue = taskQueue
}

// TaskQueue returns the task queue name set via [SetTaskQueue]. It may return an empty string if
// the task queue has not been set yet (it will be resolved from the environment during worker
// creation).
func (c *ConfigureWorkerContext) TaskQueue() string {
	return c.taskQueue
}

// replayRegistrations replays all buffered registrations onto the given worker.
func (c *ConfigureWorkerContext) replayRegistrations(w worker.Worker) {
	for _, fn := range c.registrations {
		fn(w)
	}
}
