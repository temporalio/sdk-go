package googleadk_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// childResults records what each tool got back from the child workflow it
// awaited, keyed by tool name. Written from in-workflow tool code on the
// workflow's own goroutine and read after the run completes.
var childResults struct {
	mu sync.Mutex
	m  map[string]string
}

func recordChildResult(name, out string) {
	childResults.mu.Lock()
	defer childResults.mu.Unlock()
	if childResults.m == nil {
		childResults.m = map[string]string{}
	}
	childResults.m[name] = out
}

func takeChildResults() map[string]string {
	childResults.mu.Lock()
	defer childResults.mu.Unlock()
	out := childResults.m
	childResults.m = nil
	return out
}

// echoChild stands in for a sub-agent running as its own workflow.
func echoChild(_ workflow.Context, msg string) (string, error) {
	return "child:" + msg, nil
}

// failingChild always fails, so a tool can observe a child's failure.
func failingChild(_ workflow.Context, _ string) (string, error) {
	return "", temporal.NewNonRetryableApplicationError("child exploded", "ChildError", nil)
}

// childSpawningTool is an in-workflow function tool that starts a child
// workflow and blocks on its result — the pattern WorkflowContext enables.
// Under concurrent fan-out it must dispatch on the task's own coroutine rather
// than on the enclosing workflow function's root Context.
func childSpawningTool(name string, child any) (tool.Tool, error) {
	return funcTool(name, func(actx agent.Context, _ map[string]any) (map[string]any, error) {
		wfCtx, ok := googleadk.WorkflowContext(actx)
		if !ok {
			return nil, errors.New("no workflow.Context on the ADK context")
		}
		cctx := workflow.WithChildOptions(wfCtx, workflow.ChildWorkflowOptions{
			WorkflowID:               workflow.GetInfo(wfCtx).WorkflowExecution.ID + "-" + name,
			WorkflowExecutionTimeout: time.Minute,
		})
		var out string
		if err := workflow.ExecuteChildWorkflow(cctx, child, name).Get(cctx, &out); err != nil {
			return nil, err
		}
		recordChildResult(name, out)
		return map[string]any{"child": out}, nil
	})
}

// childFanoutWorkflow drives the agent with two child-spawning tools, so one
// LLM turn fans out to two tools that each start and await their own child.
func childFanoutWorkflow(ctx workflow.Context, sequential bool) (runResult, error) {
	t1, err := childSpawningTool("t1", echoChild)
	if err != nil {
		return runResult{}, err
	}
	t2, err := childSpawningTool("t2", echoChild)
	if err != nil {
		return runResult{}, err
	}
	var ctxOpts []googleadk.ContextOption
	if sequential {
		ctxOpts = append(ctxOpts, googleadk.WithSequentialToolFanout())
	}
	return runAgent(ctx, agentBuild{
		ctxOpts:     ctxOpts,
		modelName:   "fake-model",
		userMessage: "call both",
		tools:       []tool.Tool{t1, t2},
	})
}

// rootContextWorkflow is the anti-pattern the accessor exists to avoid: each
// tool ignores its own coroutine's Context and blocks a child-workflow Future
// on the root Context captured from the enclosing workflow function.
func rootContextWorkflow(ctx workflow.Context) (runResult, error) {
	spawnOnRoot := func(name string) (tool.Tool, error) {
		return funcTool(name, func(agent.Context, map[string]any) (map[string]any, error) {
			var out string
			// ctx is the ROOT context, captured by closure — the wrong coroutine.
			if err := workflow.ExecuteChildWorkflow(ctx, echoChild, name).Get(ctx, &out); err != nil {
				return nil, err
			}
			return map[string]any{"child": out}, nil
		})
	}
	t1, err := spawnOnRoot("t1")
	if err != nil {
		return runResult{}, err
	}
	t2, err := spawnOnRoot("t2")
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		modelName:   "fake-model",
		userMessage: "call both",
		tools:       []tool.Tool{t1, t2},
	})
}

// failingChildWorkflow gives the tool a child that fails, so the test can
// assert the failure reaches the tool rather than silently resolving.
func failingChildWorkflow(ctx workflow.Context) (runResult, error) {
	t1, err := childSpawningTool("t1", failingChild)
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		modelName:   "fake-model",
		userMessage: "call it",
		tools:       []tool.Tool{t1},
	})
}

func newChildEnv(t *testing.T, s *testsuite.WorkflowTestSuite, wf any, first *model.LLMResponse) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(wf)
	env.RegisterWorkflow(echoChild)
	env.RegisterWorkflow(failingChild)
	wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(first, googleadk.TextResponse("done")),
		},
	})
	return env
}

// TestWorkflowContextStartsChildFromConcurrentFanout is the case that cannot be
// written without the exported accessor: two in-workflow tools, dispatched
// concurrently by the fan-out task runner, each recover their own coroutine's
// workflow.Context and use it to start and await a child workflow.
func TestWorkflowContextStartsChildFromConcurrentFanout(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := newChildEnv(t, &s, childFanoutWorkflow, twoFunctionCalls())

	env.ExecuteWorkflow(childFanoutWorkflow, false)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.ElementsMatch(t, []string{"t1", "t2"}, res.ToolResponses, "both tools answered")
	assert.Equal(t, map[string]string{"t1": "child:t1", "t2": "child:t2"},
		takeChildResults(), "each tool awaited its own child's result")
}

// TestWorkflowContextStartsChildFromSequentialFanout is the same assertion on
// the sequential path, where tasks run on the calling coroutine — so the
// accessor must return that coroutine's Context there too.
func TestWorkflowContextStartsChildFromSequentialFanout(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := newChildEnv(t, &s, childFanoutWorkflow, twoFunctionCalls())

	env.ExecuteWorkflow(childFanoutWorkflow, true)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.ElementsMatch(t, []string{"t1", "t2"}, res.ToolResponses, "both tools answered")
	assert.Equal(t, map[string]string{"t1": "child:t1", "t2": "child:t2"},
		takeChildResults(), "each tool awaited its own child's result")
}

// TestWorkflowContextChildIsRecordedAsExecution proves the tool issued a real
// child-workflow command rather than something that merely returned the right
// value: each child starts as its own execution, with the id the tool chose.
func TestWorkflowContextChildIsRecordedAsExecution(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := newChildEnv(t, &s, childFanoutWorkflow, twoFunctionCalls())

	var startedIDs []string
	env.SetOnChildWorkflowStartedListener(func(info *workflow.Info, _ workflow.Context, _ converter.EncodedValues) {
		startedIDs = append(startedIDs, info.WorkflowExecution.ID)
	})

	env.ExecuteWorkflow(childFanoutWorkflow, false)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Len(t, startedIDs, 2, "each tool started exactly one child")
	for _, id := range startedIDs {
		assert.True(t, strings.HasSuffix(id, "-t1") || strings.HasSuffix(id, "-t2"),
			"the child id comes from the tool's ChildWorkflowOptions: %s", id)
	}
	assert.Equal(t, map[string]string{"t1": "child:t1", "t2": "child:t2"}, takeChildResults())
}

// TestWorkflowContextChildFailureReachesTool proves a failing child surfaces as
// an error to the tool instead of resolving silently.
func TestWorkflowContextChildFailureReachesTool(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := newChildEnv(t, &s, failingChildWorkflow,
		googleadk.FunctionCallResponse("c1", "t1", map[string]any{}))

	env.ExecuteWorkflow(failingChildWorkflow)
	require.True(t, env.IsWorkflowCompleted())

	assert.Empty(t, takeChildResults(), "a failing child records no result")

	// The failure must be visible: either the run errors, or ADK reports the
	// tool error back to the model. What matters is that it is not dropped.
	if err := env.GetWorkflowError(); err != nil {
		assert.Contains(t, err.Error(), "child exploded")
		return
	}
	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.NotEmpty(t, res.ToolResponses, "the tool error is reported back to the model")
}

// TestWorkflowContextRootContextFailsOnFanout documents WHY the accessor is
// needed, and is the reason a tool cannot simply close over the workflow
// function's Context. A tool that blocks a child-workflow Future on the root
// Context while the fan-out runner is running it on another coroutine hangs the
// dispatcher: the coroutine that owns the root Context is already blocked on
// the fan-out join, so the Future can never be resolved from there and the
// workflow task trips deadlock detection (TMPRL1101) instead of progressing.
//
// This is the failure mode every alternative to the accessor lands in —
// redeclaring the unexported key (which cannot retrieve the value at all), or
// installing a custom platform.WithTaskRunner (whose key the adapter's own
// model and tool proxies do not read).
func TestWorkflowContextRootContextFailsOnFanout(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := newChildEnv(t, &s, rootContextWorkflow, twoFunctionCalls())

	env.ExecuteWorkflow(rootContextWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err, "blocking on the root Context from a fan-out coroutine must not succeed")
	assert.Contains(t, err.Error(), "deadlock",
		"the owning coroutine is already blocked on the fan-out join, so the child Future never resolves: %v", err)
}

// TestWorkflowContextAbsentOutsideWorkflow proves the accessor reports absent
// (rather than panicking, or handing back a nil Context that panics later) for
// a context that did not come from NewContext — so a tool can fall back to a
// non-durable path, as the model and tool proxies already do.
func TestWorkflowContextAbsentOutsideWorkflow(t *testing.T) {
	_, ok := googleadk.WorkflowContext(t.Context())
	assert.False(t, ok, "a plain context carries no workflow.Context")

	//nolint:staticcheck // asserting the nil-input contract explicitly
	_, ok = googleadk.WorkflowContext(nil)
	assert.False(t, ok, "a nil context reports absent rather than panicking")
}

// TestWorkflowContextReturnsRootOutsideFanout proves that outside tool fan-out
// the accessor yields a usable Context — the root one stashed by NewContext —
// so a tool invoked on its own can still issue workflow commands.
func TestWorkflowContextReturnsRootOutsideFanout(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()

	wf := func(ctx workflow.Context) (bool, error) {
		adkCtx := googleadk.NewContext(ctx)
		got, ok := googleadk.WorkflowContext(adkCtx)
		if !ok {
			return false, errors.New("NewContext did not stash a workflow.Context")
		}
		return workflow.GetInfo(got).WorkflowExecution.ID == workflow.GetInfo(ctx).WorkflowExecution.ID, nil
	}
	env.RegisterWorkflow(wf)
	env.ExecuteWorkflow(wf)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var same bool
	require.NoError(t, env.GetWorkflowResult(&same))
	assert.True(t, same, "the accessor returns the calling workflow's own Context")
}
