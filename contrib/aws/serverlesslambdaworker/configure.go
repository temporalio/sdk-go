package serverlesslambdaworker

import (
	"github.com/nexus-rpc/sdk-go/nexus"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ConfigureWorkerContext is passed to the configure callback of [RunWorker].
// It collects workflow, activity, and Nexus service registrations as well as
// client and worker option overrides. Registrations are buffered and replayed
// onto the worker after it is created.
//
// ConfigureWorkerContext has unexported fields for forward-compatibility.
type ConfigureWorkerContext struct {
	taskQueue           string
	mutateClientOpts    func(*client.Options)
	mutateWorkerOpts    func(*worker.Options)
	registrations       []registration
}

type registrationKind int

const (
	regWorkflow registrationKind = iota
	regWorkflowWithOptions
	regActivity
	regActivityWithOptions
	regNexusService
)

type registration struct {
	kind            registrationKind
	target          interface{}
	workflowOpts   workflow.RegisterOptions
	activityOpts   activity.RegisterOptions
	nexusService    *nexus.Service
}

// RegisterWorkflow registers a workflow function with the worker.
func (c *ConfigureWorkerContext) RegisterWorkflow(w interface{}) {
	c.registrations = append(c.registrations, registration{kind: regWorkflow, target: w})
}

// RegisterWorkflowWithOptions registers a workflow function with the given options.
func (c *ConfigureWorkerContext) RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions) {
	c.registrations = append(c.registrations, registration{kind: regWorkflowWithOptions, target: w, workflowOpts: options})
}

// RegisterActivity registers an activity function or struct with the worker.
func (c *ConfigureWorkerContext) RegisterActivity(a interface{}) {
	c.registrations = append(c.registrations, registration{kind: regActivity, target: a})
}

// RegisterActivityWithOptions registers an activity function or struct with the given options.
func (c *ConfigureWorkerContext) RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions) {
	c.registrations = append(c.registrations, registration{kind: regActivityWithOptions, target: a, activityOpts: options})
}

// RegisterNexusService registers a Nexus service with the worker.
func (c *ConfigureWorkerContext) RegisterNexusService(s *nexus.Service) {
	c.registrations = append(c.registrations, registration{kind: regNexusService, nexusService: s})
}

// MutateClientOptions sets a callback that mutates client options before
// [client.Dial] is called. The callback receives options that already have
// Lambda defaults applied, so any values the callback sets will override
// Lambda defaults.
func (c *ConfigureWorkerContext) MutateClientOptions(fn func(*client.Options)) {
	c.mutateClientOpts = fn
}

// MutateWorkerOptions sets a callback that mutates worker options before
// [worker.New] is called. The callback receives options that already have
// Lambda defaults applied, so any values the callback sets will override
// Lambda defaults.
func (c *ConfigureWorkerContext) MutateWorkerOptions(fn func(*worker.Options)) {
	c.mutateWorkerOpts = fn
}

// SetTaskQueue sets the task queue name for the worker. If not set, the
// TEMPORAL_TASK_QUEUE environment variable is used.
func (c *ConfigureWorkerContext) SetTaskQueue(taskQueue string) {
	c.taskQueue = taskQueue
}

// TaskQueue returns the task queue name set via [SetTaskQueue]. It may return
// an empty string if the task queue has not been set yet (it will be resolved
// from the environment during worker creation).
func (c *ConfigureWorkerContext) TaskQueue() string {
	return c.taskQueue
}

// replayRegistrations replays all buffered registrations onto the given worker.
func (c *ConfigureWorkerContext) replayRegistrations(w worker.Worker) {
	for _, r := range c.registrations {
		switch r.kind {
		case regWorkflow:
			w.RegisterWorkflow(r.target)
		case regWorkflowWithOptions:
			w.RegisterWorkflowWithOptions(r.target, r.workflowOpts)
		case regActivity:
			w.RegisterActivity(r.target)
		case regActivityWithOptions:
			w.RegisterActivityWithOptions(r.target, r.activityOpts)
		case regNexusService:
			w.RegisterNexusService(r.nexusService)
		}
	}
}
