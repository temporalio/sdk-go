package googleadk

import (
	"context"
	"sync"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
)

// NewPlugin wires the integration as a Temporal worker plugin: it builds the
// worker-side Activities from cfg, registers InvokeModel, ListMcpTools and
// CallMcpTool at worker start, and closes the cached MCP toolsets at worker
// stop. Add it to worker.Options.Plugins:
//
//	adkPlugin, err := googleadk.NewPlugin(googleadk.Config{
//		Models:      map[string]googleadk.ModelFactory{ /* ... */ },
//		MCPToolsets: map[string]googleadk.MCPFactory{ /* ... */ },
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	w := worker.New(c, taskQueue, worker.Options{
//		Plugins: []worker.Plugin{adkPlugin},
//	})
//
// Create one plugin per worker: after its worker stops, the plugin's MCP
// toolsets are closed (Activities.Close is terminal), so the same plugin value
// must not serve a second worker. Toolset close failures at worker stop are
// discarded — construct the Activities yourself and call Close if you need to
// handle them. For the workflow/activity test environments use NewActivities +
// Register instead: test environments construct no real worker, so plugins do
// not run there.
func NewPlugin(cfg Config) (worker.Plugin, error) {
	acts, err := NewActivities(cfg)
	if err != nil {
		return nil, err
	}
	// A plugin can be shared between a worker and a workflow replayer, and the
	// run-context hooks fire once per workflow replay too. replayerKeys records
	// which run contexts are replays so RunContextAfter never closes the
	// worker's shared toolsets on a replay: Close is terminal, so closing there
	// would fail every later MCP Activity on the worker non-retryably. Entries
	// are counted, not merely set, because every replay on one replayer shares
	// its instance key — concurrent replays must each pair their own
	// before/after without the last one falling through to Close.
	var mu sync.Mutex
	replayerKeys := make(map[string]int)
	return temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name: "go.temporal.io/sdk/contrib/googleadk",
		RunContextBefore: func(_ context.Context, o temporal.SimplePluginRunContextBeforeOptions) error {
			if o.WorkflowReplayer {
				// Activity registration is a no-op on a replayer; just record
				// the key so RunContextAfter can tell this run was a replay.
				mu.Lock()
				replayerKeys[o.InstanceKey]++
				mu.Unlock()
				return nil
			}
			acts.registerAll(o.Registry)
			return nil
		},
		RunContextAfter: func(_ context.Context, o temporal.SimplePluginRunContextAfterOptions) {
			mu.Lock()
			replay := replayerKeys[o.InstanceKey] > 0
			if replay {
				if replayerKeys[o.InstanceKey]--; replayerKeys[o.InstanceKey] == 0 {
					delete(replayerKeys, o.InstanceKey)
				}
			}
			mu.Unlock()
			if replay {
				return
			}
			_ = acts.Close()
		},
	})
}
