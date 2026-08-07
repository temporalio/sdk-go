package googleadk_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// pluginRecordingRegistry implements the plugin run-context registry (the full
// worker.PluginStartWorkerOptions.WorkerRegistry interface) and records the
// name of every registered activity, so a test can assert exactly what
// StartWorker wired.
type pluginRecordingRegistry struct {
	activityNames []string
}

func (r *pluginRecordingRegistry) RegisterWorkflowWithOptions(any, workflow.RegisterOptions) {}

func (r *pluginRecordingRegistry) RegisterDynamicWorkflow(any, workflow.DynamicRegisterOptions) {}

func (r *pluginRecordingRegistry) RegisterActivityWithOptions(_ any, options activity.RegisterOptions) {
	r.activityNames = append(r.activityNames, options.Name)
}

func (r *pluginRecordingRegistry) RegisterDynamicActivity(any, activity.DynamicRegisterOptions) {}

func (r *pluginRecordingRegistry) RegisterNexusService(*nexus.Service) {}

// pluginNoopReplayRegistry is the minimal WorkflowReplayRegistry for driving a
// plugin's ReplayWorkflow hook directly: workflow registration is a no-op.
type pluginNoopReplayRegistry struct{}

func (pluginNoopReplayRegistry) RegisterWorkflowWithOptions(any, workflow.RegisterOptions) {}

func (pluginNoopReplayRegistry) RegisterDynamicWorkflow(any, workflow.DynamicRegisterOptions) {}

// pluginClosableToolset wraps a toolset with a counting Close method, so a test
// can observe the plugin's worker-stop hook reaching the cached toolset.
type pluginClosableToolset struct {
	tool.Toolset
	closed atomic.Int32
}

func (t *pluginClosableToolset) Close() error {
	t.closed.Add(1)
	return nil
}

// pluginSearchServer builds the fake MCP server the plugin tests share: one
// "search" tool with a constant result.
func pluginSearchServer() *googleadk.FakeMCPServer {
	return googleadk.NewFakeMCPServer("search-server").AddTool(
		"search", "search the web",
		&genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"query": {Type: genai.TypeString, Description: "the search query"},
			},
			Required: []string{"query"},
		},
		func(map[string]any) (map[string]any, error) {
			return map[string]any{"hits": 1}, nil
		},
	)
}

// newMCPPlugin builds a plugin whose Config caches ct as its one MCP toolset
// and scripts one search call followed by a final text turn.
func newMCPPlugin(t *testing.T, ct *pluginClosableToolset) worker.Plugin {
	t.Helper()
	plugin, err := googleadk.NewPlugin(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "search", map[string]any{"query": "temporal"}),
				googleadk.TextResponse("done"),
			),
		},
		MCPToolsets: map[string]googleadk.MCPFactory{
			"search-server": func(context.Context) (tool.Toolset, error) { return ct, nil },
		},
	})
	require.NoError(t, err)
	return plugin
}

// startPluginWorker drives the plugin's StartWorker hook — the same hook a real
// worker start invokes — registering into reg, and requires next to be called.
func startPluginWorker(t *testing.T, plugin worker.Plugin, key string, reg interface {
	RegisterWorkflowWithOptions(any, workflow.RegisterOptions)
	RegisterDynamicWorkflow(any, workflow.DynamicRegisterOptions)
	RegisterActivityWithOptions(any, activity.RegisterOptions)
	RegisterDynamicActivity(any, activity.DynamicRegisterOptions)
	RegisterNexusService(*nexus.Service)
}) {
	t.Helper()
	var nextCalled bool
	require.NoError(t, plugin.StartWorker(context.Background(),
		worker.PluginStartWorkerOptions{WorkerInstanceKey: key, WorkerRegistry: reg},
		func(context.Context, worker.PluginStartWorkerOptions) error {
			nextCalled = true
			return nil
		},
	))
	require.True(t, nextCalled, "StartWorker must invoke next")
}

// stopPluginWorker drives the plugin's StopWorker hook — the same hook a real
// worker stop invokes — and requires next to be called.
func stopPluginWorker(t *testing.T, plugin worker.Plugin, key string) {
	t.Helper()
	var nextCalled bool
	plugin.StopWorker(context.Background(),
		worker.PluginStopWorkerOptions{WorkerInstanceKey: key},
		func(context.Context, worker.PluginStopWorkerOptions) { nextCalled = true },
	)
	require.True(t, nextCalled, "StopWorker must invoke next")
}

// runSearchWorkflow executes agentRunWorkflow with the scripted MCP search
// against env and requires it to complete with the tool having run.
func runSearchWorkflow(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "search once",
		MCPToolset:  "search-server",
	})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.ToolResponses, "search", "the MCP tool must have run")
}

// TestNewPluginRejectsInvalidConfig proves NewPlugin surfaces a Config error at
// plugin construction (worker wiring time) rather than mid-run in an Activity.
func TestNewPluginRejectsInvalidConfig(t *testing.T) {
	_, err := googleadk.NewPlugin(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{"broken-model": nil},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "broken-model")
}

// TestPluginRegistersActivitiesOnWorkerStart proves the plugin's StartWorker
// hook registers exactly the three integration Activities under their stable
// names and passes control on to next.
func TestPluginRegistersActivitiesOnWorkerStart(t *testing.T) {
	plugin, err := googleadk.NewPlugin(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(googleadk.TextResponse("done")),
		},
	})
	require.NoError(t, err)

	reg := &pluginRecordingRegistry{}
	startPluginWorker(t, plugin, "worker-1", reg)

	assert.ElementsMatch(t, []string{
		googleadk.InvokeModelActivityName,
		googleadk.ListMcpToolsActivityName,
		googleadk.CallMcpToolActivityName,
	}, reg.activityNames)
}

// TestPluginClosesMCPToolsetsOnWorkerStop proves the plugin lifecycle
// end-to-end: StartWorker registers the Activities (here into a test workflow
// environment), a run constructs and caches the MCP toolset, and StopWorker —
// and only StopWorker — closes it, exactly once, idempotently.
func TestPluginClosesMCPToolsetsOnWorkerStop(t *testing.T) {
	ct := &pluginClosableToolset{Toolset: pluginSearchServer()}
	plugin := newMCPPlugin(t, ct)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(agentRunWorkflow)
	startPluginWorker(t, plugin, "worker-1", testRegistry{env})

	runSearchWorkflow(t, env)
	require.Zero(t, ct.closed.Load(), "toolset must stay open while the worker runs")

	stopPluginWorker(t, plugin, "worker-1")
	assert.Equal(t, int32(1), ct.closed.Load(), "worker stop must close the cached toolset")

	stopPluginWorker(t, plugin, "worker-1")
	assert.Equal(t, int32(1), ct.closed.Load(), "a second stop must not close again")
}

// TestPluginReplayDoesNotCloseToolsets proves the replay guard: a plugin shared
// with a workflow replayer runs its after-hook once per replay, and that pass
// must neither close nor terminally poison the worker's shared Activities —
// Close is terminal, so closing on replay would fail every later MCP Activity
// on the worker non-retryably. After the replay, the full worker lifecycle
// still works and Close fires exactly once, at worker stop.
func TestPluginReplayDoesNotCloseToolsets(t *testing.T) {
	ct := &pluginClosableToolset{Toolset: pluginSearchServer()}
	plugin := newMCPPlugin(t, ct)

	require.NoError(t, plugin.ReplayWorkflow(context.Background(),
		worker.PluginReplayWorkflowOptions{
			WorkflowReplayerInstanceKey: "replayer-1",
			WorkflowReplayRegistry:      pluginNoopReplayRegistry{},
		},
		func(context.Context, worker.PluginReplayWorkflowOptions) error { return nil },
	))
	require.Zero(t, ct.closed.Load(), "a replay must not close the worker's shared toolsets")

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(agentRunWorkflow)
	startPluginWorker(t, plugin, "worker-1", testRegistry{env})

	runSearchWorkflow(t, env)
	require.Zero(t, ct.closed.Load(), "toolset must stay open while the worker runs")

	stopPluginWorker(t, plugin, "worker-1")
	assert.Equal(t, int32(1), ct.closed.Load(), "worker stop must close the cached toolset exactly once")
}
