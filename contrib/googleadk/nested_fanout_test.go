package googleadk_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/platform"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/agenttool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// nestedFanoutWorkflow reproduces ADK's nested fan-out shape directly at the
// platform seam: an outer RunTasks whose first task itself calls RunTasks with
// its per-task context — exactly what happens when an agent tool's sub-agent
// turn carries parallel function calls (platform.RunTasks hands the runner the
// invocation context, which for a nested call is the task context of the outer
// fan-out's coroutine).
func nestedFanoutWorkflow(ctx workflow.Context) error {
	adkCtx := googleadk.NewContext(ctx)

	// Inner batch: two no-op tasks fanned out from inside an outer task, so the
	// TaskRunner is re-entered on a child coroutine.
	inner := []func(context.Context){
		func(context.Context) {},
		func(context.Context) {},
	}
	outer := []func(context.Context){
		func(taskCtx context.Context) {
			// The nested join must block on the invoking (child) coroutine
			// carried by taskCtx; blocking on the parked root coroutine panics
			// the dispatcher.
			platform.RunTasks(taskCtx, inner)
		},
		func(context.Context) {},
	}
	platform.RunTasks(adkCtx, outer)
	return nil
}

// TestNestedFanoutJoinsOnInvokingCoroutine proves the TaskRunner joins nested
// fan-out on the coroutine that invoked it. Before the fix the inner join
// blocked on the captured root workflow.Context — whose coroutine was already
// parked on the outer join — and the dispatcher panicked with "trying to block
// on coroutine which is already blocked".
func TestNestedFanoutJoinsOnInvokingCoroutine(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(nestedFanoutWorkflow)
	env.ExecuteWorkflow(nestedFanoutWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// reproFnCalls builds one LLM response carrying a parallel function call per
// name. Every call includes a "request" argument so an agenttool target —
// whose default input schema requires "request" — accepts it; the no-op
// function tools simply ignore it.
func reproFnCalls(names ...string) *model.LLMResponse {
	parts := make([]*genai.Part, len(names))
	for i, name := range names {
		parts[i] = &genai.Part{FunctionCall: &genai.FunctionCall{
			ID:   fmt.Sprintf("call-%d", i+1),
			Name: name,
			Args: map[string]any{"request": "go"},
		}}
	}
	return &model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: parts}}
}

// nestedAgentToolWorkflow drives the real nested-agent machinery end to end:
// the root turn fans out to an agent tool plus a plain tool, and the agent
// tool's sub-agent turn fans out again to two tools of its own, re-entering
// the TaskRunner from inside a task coroutine.
func nestedAgentToolWorkflow(ctx workflow.Context) (runResult, error) {
	suba, err := noopTool("suba", "sub tool a")
	if err != nil {
		return runResult{}, err
	}
	subb, err := noopTool("subb", "sub tool b")
	if err != nil {
		return runResult{}, err
	}
	sub, err := llmagent.New(llmagent.Config{
		Name:        "helper",
		Description: "helper sub-agent",
		Model:       googleadk.NewModel("fake-sub"),
		Instruction: "run both tools",
		Tools:       []tool.Tool{suba, subb},
	})
	if err != nil {
		return runResult{}, err
	}
	side, err := noopTool("side", "side tool")
	if err != nil {
		return runResult{}, err
	}
	return runAgent(ctx, agentBuild{
		modelName:   "fake-root",
		userMessage: "use the helper and the side tool",
		tools:       []tool.Tool{agenttool.New(sub, nil), side},
	})
}

// TestNestedAgentToolFanout exercises nested concurrent fan-out through the
// real ADK agent tree: an agent tool whose sub-agent's turn itself fans out.
// Before the fix ADK's node runtime recovered the dispatcher panic and the
// workflow wedged instead (surfacing as a schedule-to-close timeout once the
// test environment auto-fired its long timer); after it, the run completes
// with both fan-out levels executed.
func TestNestedAgentToolFanout(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(nestedAgentToolWorkflow)
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-root": scriptedModelFactory(reproFnCalls("helper", "side"), googleadk.TextResponse("root done")),
			"fake-sub":  scriptedModelFactory(reproFnCalls("suba", "subb"), googleadk.TextResponse("sub done")),
		},
	})
	env.ExecuteWorkflow(nestedAgentToolWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res runResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Contains(t, res.Texts, "root done")
	assert.Contains(t, res.ToolResponses, "helper")
	assert.Contains(t, res.ToolResponses, "side")
	// Two root turns (fan-out + final text) and two sub-agent turns.
	assert.Equal(t, 4, counter.get(googleadk.InvokeModelActivityName))
}
