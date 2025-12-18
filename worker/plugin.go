package worker

import "go.temporal.io/sdk/internal"

// Plugin is a plugin that can configure worker/replayer options and
// surround worker/replayer runs. Many plugin implementers may prefer the
// simpler [go.temporal.io/sdk/temporal.SimplePlugin] instead.
//
// All worker plugins must embed [go.temporal.io/sdk/worker.PluginBase]. All
// plugins must implement Name().
//
// NOTE: Experimental
type Plugin = internal.WorkerPlugin

// PluginBase must be embedded into worker plugin implementations.
//
// NOTE: Experimental
type PluginBase = internal.WorkerPluginBase

// PluginConfigureWorkerOptions are options for ConfigureWorker on a
// worker plugin.
//
// NOTE: Experimental
type PluginConfigureWorkerOptions = internal.WorkerPluginConfigureWorkerOptions

// PluginConfigureWorkerRegistryOptions are the set of callbacks that can
// be adjusted by plugins when configuring workers. If adjusting a callback that
// is already set, implementers may want to take care to invoke the existing
// callback inside their own.
//
// NOTE: Experimental
type PluginConfigureWorkerRegistryOptions = internal.WorkerPluginConfigureWorkerRegistryOptions

// PluginStartWorkerOptions are options for StartWorker on a worker
// plugin.
//
// NOTE: Experimental
type PluginStartWorkerOptions = internal.WorkerPluginStartWorkerOptions

// PluginStopWorkerOptions are options for StopWorker on a worker plugin.
//
// NOTE: Experimental
type PluginStopWorkerOptions = internal.WorkerPluginStopWorkerOptions

// PluginConfigureWorkflowReplayerOptions are options for
// ConfigureWorkflowReplayer on a worker plugin.
//
// NOTE: Experimental
type PluginConfigureWorkflowReplayerOptions = internal.WorkerPluginConfigureWorkflowReplayerOptions

// PluginConfigureWorkflowReplayerRegistryOptions are the set of callbacks
// that can be adjusted by plugins when configuring workflow replayers. If
// adjusting a callback that is already set, implementers may want to take care
// to invoke the existing callback inside their own.
//
// NOTE: Experimental
type PluginConfigureWorkflowReplayerRegistryOptions = internal.WorkerPluginConfigureWorkflowReplayerRegistryOptions

// PluginReplayWorkflowOptions are options for ReplayWorkflow on a worker
// plugin.
//
// NOTE: Experimental
type PluginReplayWorkflowOptions = internal.WorkerPluginReplayWorkflowOptions
