package googleadk_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// TestFunctionToolRunsInWorkflow is an end-to-end test of the ordinary tool path:
// the model scripts a function call, ADK runs the (real) function tool
// in-workflow on the deterministic dispatcher, its result is fed back to the
// model, and the model's next turn produces the final answer. The tool's effect
// (the args it saw) is asserted directly — no CallTool Activity is involved,
// because ordinary function tools no longer cross the Activity boundary.
func TestFunctionToolRunsInWorkflow(t *testing.T) {
	var gotArgs map[string]any
	var mu sync.Mutex
	echo := recordingTool(t, "echo", map[string]any{"echoed": "hi"}, func(args map[string]any) {
		mu.Lock()
		gotArgs = args
		mu.Unlock()
	})

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(echoToolWorkflow)
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "echo", map[string]any{"msg": "hi"}),
				googleadk.TextResponse("done"),
			),
		},
	})
	echoTool = echo

	env.ExecuteWorkflow(echoToolWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.FunctionCalls, "echo")
	assert.Contains(t, res.ToolResponses, "echo")
	assert.Contains(t, res.Texts, "done")

	mu.Lock()
	assert.Equal(t, "hi", gotArgs["msg"])
	mu.Unlock()

	// Two model turns (initial + after tool). The tool itself ran in-workflow, so
	// no tool Activity was scheduled.
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName))
}

// echoTool is set by TestFunctionToolRunsInWorkflow before executing
// echoToolWorkflow (the test does not run in parallel), so the in-workflow tool
// can carry a test-owned recording handler across the workflow boundary.
var echoTool tool.Tool

// echoToolWorkflow drives the agent with the test's recording echo tool.
func echoToolWorkflow(ctx workflow.Context) (runResult, error) {
	return runAgent(ctx, agentBuild{
		modelName:   "fake-model",
		userMessage: "please echo",
		tools:       []tool.Tool{echoTool},
	})
}

// TestActivityAsToolRoundTrip proves the ActivityAsTool adopter primitive: an
// existing Temporal activity is exposed to the agent as a tool and dispatched
// directly by its own activity name when the model calls it.
func TestActivityAsToolRoundTrip(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(activityAsToolWorkflow)
	env.RegisterActivityWithOptions(weatherActivity, activity.RegisterOptions{Name: "weather"})

	// The model registry still serves the LLM turns.
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "weather", map[string]any{"city": "Paris"}),
				googleadk.TextResponse("it is sunny in Paris"),
			),
		},
	})

	env.ExecuteWorkflow(activityAsToolWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "it is sunny in Paris")
	// The user's own activity was dispatched directly, by its own name.
	assert.Equal(t, 1, counter.get("weather"))
}

// TestInWorkflowToolMutatesState is a regression for a fixed bug: an in-workflow
// function tool that mutates session state via ctx.State().Set now has its
// mutation persist into the resulting session (it was silently dropped when
// tools ran in an Activity over a read-only snapshot). The handler flips a flag
// so we also prove it ran, and the run completes cleanly.
func TestInWorkflowToolMutatesState(t *testing.T) {
	inWorkflowToolRan.Store(false)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(stateMutationWorkflow)
	// "setflag" is intentionally NOT registered worker-side: it can only succeed
	// by running in-workflow, so a clean completion proves the in-workflow path.
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "setflag", map[string]any{}),
				googleadk.TextResponse("done"),
			),
		},
	})

	env.ExecuteWorkflow(stateMutationWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.FunctionCalls, "setflag")
	assert.Contains(t, res.ToolResponses, "setflag")
	assert.Contains(t, res.Texts, "done")
	assert.True(t, inWorkflowToolRan.Load(), "in-workflow tool handler must have executed")
	// The mutation the in-workflow tool made is visible in the resulting session.
	assert.Equal(t, "set-in-workflow", res.StateFlag,
		"in-workflow tool state mutation must persist into the session")
	assert.Equal(t, 2, counter.get(googleadk.InvokeModelActivityName))
}

// ----------------------------------------------------------------------------
// Helpers specific to the tool tests.
// ----------------------------------------------------------------------------

// weatherArgs is the input struct for weatherActivity; ActivityAsTool infers the
// tool's parameter schema from it.
type weatherArgs struct {
	City string `json:"city"`
}

// weatherActivity is an ordinary Temporal activity the user already owns.
func weatherActivity(_ context.Context, in weatherArgs) (map[string]any, error) {
	return map[string]any{"forecast": "sunny in " + in.City}, nil
}

// activityAsToolWorkflow wires weatherActivity into the agent via ActivityAsTool.
func activityAsToolWorkflow(ctx workflow.Context) (runResult, error) {
	weatherTool, err := googleadk.ActivityAsTool(weatherActivity, googleadk.ActivityToolOptions{
		Name:        "weather",
		Description: "look up the weather for a city",
		ActivityOptions: workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		},
	})
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		modelName:   "fake-model",
		userMessage: "weather in Paris",
		tools:       []tool.Tool{weatherTool},
	})
}

// stateMutationWorkflow registers an in-workflow function tool that writes to
// session state via ctx.State().Set, runs the agent against a session service it
// owns, then reads the session back with ExportSession and reports the mutated
// value — proving in-workflow tools' state changes persist into the session
// (they were silently dropped when tools ran in an Activity over a read-only
// snapshot).
func stateMutationWorkflow(ctx workflow.Context) (runResult, error) {
	setflag, err := funcTool("setflag", func(tctx agent.Context, _ map[string]any) (map[string]any, error) {
		inWorkflowToolRan.Store(true)
		if serr := tctx.State().Set("flag", "set-in-workflow"); serr != nil {
			return nil, serr
		}
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		return runResult{}, err
	}

	svc := session.InMemoryService()
	res, err := runAgent(ctx, agentBuild{
		modelName:      "fake-model",
		userMessage:    "set the flag",
		tools:          []tool.Tool{setflag},
		sessionService: svc,
		appName:        "test-app",
		userID:         "user-1",
		sessionID:      "session-1",
	})
	if err != nil {
		return res, err
	}

	snap, err := googleadk.ExportSession(googleadk.NewContext(ctx), svc, "test-app", "user-1", "session-1")
	if err != nil {
		return res, err
	}
	if v, ok := snap.State["flag"].(string); ok {
		res.StateFlag = v
	}
	return res, nil
}
