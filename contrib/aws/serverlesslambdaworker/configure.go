package serverlesslambdaworker

import (
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
// it. Registrations are buffered and replayed onto the real worker after it is created.
//
// ConfigureWorkerContext has unexported fields for forward-compatibility.
type ConfigureWorkerContext struct {
	taskQueue        string
	mutateClientOpts func(*client.Options)
	mutateWorkerOpts func(*worker.Options)
	registrations    []func(worker.Registry)
}

// RegisterWorkflow buffers a workflow registration to be replayed onto the worker. See
// [worker.WorkflowRegistry.RegisterWorkflow] for details.
func (c *ConfigureWorkerContext) RegisterWorkflow(w interface{}) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterWorkflow(w) })
}

// RegisterWorkflowWithOptions buffers a workflow registration with options to be replayed onto the
// worker. See [worker.WorkflowRegistry.RegisterWorkflowWithOptions] for details.
func (c *ConfigureWorkerContext) RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterWorkflowWithOptions(w, options) })
}

// RegisterDynamicWorkflow buffers a dynamic workflow registration to be replayed onto the worker.
// See [worker.WorkflowRegistry.RegisterDynamicWorkflow] for details.
func (c *ConfigureWorkerContext) RegisterDynamicWorkflow(w interface{}, options workflow.DynamicRegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterDynamicWorkflow(w, options) })
}

// RegisterActivity buffers an activity registration to be replayed onto the worker. See
// [worker.ActivityRegistry.RegisterActivity] for details.
func (c *ConfigureWorkerContext) RegisterActivity(a interface{}) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterActivity(a) })
}

// RegisterActivityWithOptions buffers an activity registration with options to be replayed onto the
// worker. See [worker.ActivityRegistry.RegisterActivityWithOptions] for details.
func (c *ConfigureWorkerContext) RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterActivityWithOptions(a, options) })
}

// RegisterDynamicActivity buffers a dynamic activity registration to be replayed onto the worker.
// See [worker.ActivityRegistry.RegisterDynamicActivity] for details.
func (c *ConfigureWorkerContext) RegisterDynamicActivity(a interface{}, options activity.DynamicRegisterOptions) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterDynamicActivity(a, options) })
}

// RegisterNexusService buffers a Nexus service registration to be replayed onto the worker. See
// [worker.NexusServiceRegistry.RegisterNexusService] for details.
func (c *ConfigureWorkerContext) RegisterNexusService(s *nexus.Service) {
	c.registrations = append(c.registrations, func(r worker.Registry) { r.RegisterNexusService(s) })
}

// MutateClientOptions sets a callback that mutates client options before [client.Dial] is called.
// The callback receives options that already have Lambda defaults applied, so any values the
// callback sets will override Lambda defaults.
func (c *ConfigureWorkerContext) MutateClientOptions(fn func(*client.Options)) {
	c.mutateClientOpts = fn
}

// MutateWorkerOptions sets a callback that mutates worker options before [worker.New] is called.
// The callback receives options that already have Lambda defaults applied, so any values the
// callback sets will override Lambda defaults.
func (c *ConfigureWorkerContext) MutateWorkerOptions(fn func(*worker.Options)) {
	c.mutateWorkerOpts = fn
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
