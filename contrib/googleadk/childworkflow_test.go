package googleadk_test

import (
	"errors"
	"strings"
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
	var startedNames []string
	env.SetOnChildWorkflowStartedListener(func(info *workflow.Info, _ workflow.Context, _ converter.EncodedValues) {
		parentID := ""
		if info.ParentWorkflowExecution != nil {
			parentID = info.ParentWorkflowExecution.ID + "-"
		}
		startedNames = append(startedNames, strings.TrimPrefix(info.WorkflowExecution.ID, parentID))
	})

	env.ExecuteWorkflow(childFanoutWorkflow, false)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.ElementsMatch(t, []string{"t1", "t2"}, res.ToolResponses, "both tools answered")
	assert.Equal(t, map[string]map[string]any{
		"t1": {"child": "child:t1"},
		"t2": {"child": "child:t2"},
	}, res.ToolResponsePayloads, "each tool awaited its own child's result")
	assert.ElementsMatch(t, []string{"t1", "t2"}, startedNames,
		"each tool starts one child with the id it chose")
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
	assert.Equal(t, map[string]map[string]any{
		"t1": {"child": "child:t1"},
		"t2": {"child": "child:t2"},
	}, res.ToolResponsePayloads, "each tool awaited its own child's result")
}

// TestWorkflowContextChildFailureReachesTool proves a failing child surfaces as
// an error to the tool instead of resolving silently.
func TestWorkflowContextChildFailureReachesTool(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := newChildEnv(t, &s, failingChildWorkflow,
		googleadk.FunctionCallResponse("c1", "t1", map[string]any{}))

	env.ExecuteWorkflow(failingChildWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	response, ok := res.ToolResponsePayloads["t1"]
	require.True(t, ok, "the tool error must be reported back to the model")
	toolErr, ok := response["error"].(string)
	require.True(t, ok, "the tool response must contain an error string")
	assert.Contains(t, toolErr, "child exploded")
}

// TestWorkflowContextAbsentOutsideWorkflow proves a tool can detect a local ADK
// run and choose a non-durable path.
func TestWorkflowContextAbsentOutsideWorkflow(t *testing.T) {
	_, ok := googleadk.WorkflowContext(t.Context())
	assert.False(t, ok, "a plain context carries no workflow.Context")
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
