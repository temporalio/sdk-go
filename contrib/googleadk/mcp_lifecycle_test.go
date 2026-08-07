package googleadk_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"

	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// countingFactory wraps an MCPFactory and counts its invocations, so a test can
// prove the worker constructs the toolset at most once per name (each extra
// construction would leak a live MCP session).
type countingFactory struct {
	calls atomic.Int32
	inner googleadk.MCPFactory
}

func (f *countingFactory) factory() googleadk.MCPFactory {
	return func(ctx context.Context) (tool.Toolset, error) {
		f.calls.Add(1)
		return f.inner(ctx)
	}
}

// closableToolset wraps a toolset with a Close method, so a test can observe
// Activities.Close reaching the cached toolset.
type closableToolset struct {
	tool.Toolset
	closed atomic.Int32
}

func (t *closableToolset) Close() error {
	t.closed.Add(1)
	return nil
}

// newLifecycleEnv mirrors newEnv/wireActivities but returns the Activities
// value itself, so a test can drive its Close method after the workflow
// completes.
func newLifecycleEnv(t *testing.T, s *testsuite.WorkflowTestSuite, cfg googleadk.Config) (*testsuite.TestWorkflowEnvironment, *googleadk.Activities) {
	t.Helper()
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(agentRunWorkflow)
	acts, err := googleadk.NewActivities(cfg)
	require.NoError(t, err)
	acts.Register(testRegistry{env})
	return env, acts
}

// searchServer builds the fake MCP server the lifecycle tests share: one
// "search" tool with a constant result.
func searchServer() *googleadk.FakeMCPServer {
	return googleadk.NewFakeMCPServer("search-server").AddTool(
		"search", "search the web", searchSchema(),
		func(map[string]any) (map[string]any, error) {
			return map[string]any{"hits": 1}, nil
		},
	)
}

// TestMcpToolsetConstructedOncePerWorker proves the MCP factory runs at most
// once per toolset name per worker: every ListMcpTools (one per turn) and
// CallMcpTool (two here) shares the one cached toolset. Without the cache each
// Activity execution would construct — and leak — its own live MCP session
// (and, for CommandTransport, a server subprocess).
func TestMcpToolsetConstructedOncePerWorker(t *testing.T) {
	cf := &countingFactory{inner: searchServer().Factory()}

	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "search", map[string]any{"query": "temporal"}),
				googleadk.FunctionCallResponse("c2", "search", map[string]any{"query": "durable execution"}),
				googleadk.TextResponse("done"),
			),
		},
		MCPToolsets: map[string]googleadk.MCPFactory{"search-server": cf.factory()},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "search twice",
		MCPToolset:  "search-server",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, int32(1), cf.calls.Load(),
		"MCP factory must run once per toolset per worker; each extra run leaks a live MCP session (and CommandTransport subprocess)")
}

// TestActivitiesCloseClosesCachedToolset proves the cached-toolset lifecycle:
// the toolset stays open across Activity calls during the run, and
// Activities.Close closes it exactly once at worker shutdown, idempotently.
func TestActivitiesCloseClosesCachedToolset(t *testing.T) {
	ct := &closableToolset{Toolset: searchServer()}

	var s testsuite.WorkflowTestSuite
	env, acts := newLifecycleEnv(t, &s, googleadk.Config{
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

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "search once",
		MCPToolset:  "search-server",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Zero(t, ct.closed.Load(), "toolset must stay open across Activity calls")

	require.NoError(t, acts.Close())
	assert.Equal(t, int32(1), ct.closed.Load())

	require.NoError(t, acts.Close())
	assert.Equal(t, int32(1), ct.closed.Load(), "Close is idempotent")
}

// TestMcpFactoryErrorNotCached proves a factory failure is not cached: the
// construct error is retryable, so Temporal re-runs ListMcpTools and the retry
// re-invokes the factory; the successful result IS cached, so the subsequent
// CallMcpTool (and later turns) never invoke it a third time.
func TestMcpFactoryErrorNotCached(t *testing.T) {
	inner := searchServer().Factory()
	var calls atomic.Int32
	flaky := func(ctx context.Context) (tool.Toolset, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("mcp server unavailable")
		}
		return inner(ctx)
	}

	var s testsuite.WorkflowTestSuite
	env, _ := newEnv(t, &s, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("c1", "search", map[string]any{"query": "temporal"}),
				googleadk.TextResponse("done"),
			),
		},
		MCPToolsets: map[string]googleadk.MCPFactory{"search-server": flaky},
	})

	env.ExecuteWorkflow(agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "search once",
		MCPToolset:  "search-server",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, int32(2), calls.Load(),
		"the construct failure must not be cached (the retry re-ran the factory) and the success must be (no third run)")
}
