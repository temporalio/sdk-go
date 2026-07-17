package googleadk_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	"go.temporal.io/sdk/contrib/googleadk"
)

// mcpGuardedRuns counts executions of the guarded MCP tool's handler. It stays
// at zero while the agent is paused awaiting confirmation, and forever after a
// denial.
var mcpGuardedRuns atomic.Int64

// guardedMCPFactory builds the worker-side factory for a fake MCP server whose
// single "guarded" tool requires human confirmation. tool.WithConfirmation
// wraps the toolset with ADK's own standard confirmation block — the identical
// ToolConfirmation / RequestConfirmation / ErrConfirmationRequired logic
// mcptoolset applies under its RequireConfirmation option — so the tests
// exercise ADK's standard worker-side confirmation path across the Activity
// boundary without a live MCP transport.
func guardedMCPFactory() googleadk.MCPFactory {
	server := googleadk.NewFakeMCPServer("guarded-server").AddTool(
		"guarded", "a guarded side-effectful tool", nil,
		func(map[string]any) (map[string]any, error) {
			mcpGuardedRuns.Add(1)
			return map[string]any{"status": "executed"}, nil
		},
	)
	inner := server.Factory()
	return func(ctx context.Context) (tool.Toolset, error) {
		ts, err := inner(ctx)
		if err != nil {
			return nil, err
		}
		return tool.WithConfirmation(ts, true, nil), nil
	}
}

// mcpHitlResult is the serializable output of mcpHitlWorkflow.
type mcpHitlResult struct {
	// Paused reports that the first Run pass paused on a pending confirmation.
	Paused bool
	// PendingHint is the hint carried by the pending confirmation.
	PendingHint string
	// OriginalCallName is the name of the tool call the confirmation wraps —
	// what the human is asked to approve.
	OriginalCallName string
	// ResumedTexts are the model texts produced on the resume pass.
	ResumedTexts []string
	// ResumedToolResponses are the function-response names seen on the resume
	// pass (the guarded tool must appear once it executes or is rejected).
	ResumedToolResponses []string
	// ResumedGuardedError is the "error" payload of the guarded tool's resumed
	// function response, when present (the denial path).
	ResumedGuardedError string
}

// mcpHitlWorkflow drives the full pause/resume HITL cycle for an MCP proxy
// tool, mirroring hitlWorkflow's two-pass loop over one shared session:
//
//	Pass 1: the model calls the guarded MCP tool; the worker-side toolset
//	        requires confirmation, so CallMcpTool returns a pending
//	        confirmation, the proxy re-records it workflow-side, and ADK emits
//	        adk_request_confirmation and pauses.
//	Pass 2: the workflow simulates the human decision (confirm) and resumes.
//	        ADK re-dispatches the original call with the decision attached; the
//	        snapshot carries it to the worker-side tool, which executes on
//	        approval or rejects on denial.
func mcpHitlWorkflow(ctx workflow.Context, confirm bool) (mcpHitlResult, error) {
	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewModel("fake-model"),
		Instruction: "be helpful",
		Toolsets:    []tool.Toolset{googleadk.NewMCPToolset(googleadk.MCPToolsetOptions{Name: "guarded-server"})},
	})
	if err != nil {
		return mcpHitlResult{}, err
	}
	r, err := runner.New(runner.Config{
		AppName:           "test-app",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return mcpHitlResult{}, err
	}

	adkCtx := googleadk.NewContext(ctx)

	// --- Pass 1: run until the agent pauses on a confirmation. ---
	var pass1 []*session.Event
	msg := genai.NewContentFromText("run the guarded tool", genai.RoleUser)
	for ev, rerr := range r.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return mcpHitlResult{}, rerr
		}
		if ev != nil {
			pass1 = append(pass1, ev)
		}
	}

	pending := googleadk.PendingConfirmations(pass1)
	var res mcpHitlResult
	if len(pending) == 0 {
		return res, nil // Not paused; the test will fail on res.Paused.
	}
	res.Paused = true
	res.PendingHint = pending[0].Hint
	if pending[0].OriginalCall != nil {
		res.OriginalCallName = pending[0].OriginalCall.Name
	}

	// --- Pass 2: simulate the human's decision, then resume. ---
	resume := googleadk.ConfirmationResponse(googleadk.ConfirmationDecision{
		FunctionCallID: pending[0].FunctionCallID,
		Confirmed:      confirm,
	})
	for ev, rerr := range r.Run(adkCtx, "user-1", "session-1", resume, agent.RunConfig{}) {
		if rerr != nil {
			return res, rerr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" {
				res.ResumedTexts = append(res.ResumedTexts, p.Text)
			}
			if p.FunctionResponse == nil {
				continue
			}
			res.ResumedToolResponses = append(res.ResumedToolResponses, p.FunctionResponse.Name)
			if p.FunctionResponse.Name == "guarded" {
				if e, ok := p.FunctionResponse.Response["error"].(string); ok {
					res.ResumedGuardedError = e
				}
			}
		}
	}
	return res, nil
}

// newMcpHitlEnv builds a test environment with mcpHitlWorkflow registered and
// the guarded MCP toolset wired.
func newMcpHitlEnv(t *testing.T, s *testsuite.WorkflowTestSuite) (*testsuite.TestWorkflowEnvironment, *activityCounter) {
	t.Helper()
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(mcpHitlWorkflow)
	counter := wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "guarded", map[string]any{"target": "prod"}),
				googleadk.TextResponse("done"),
			),
		},
		MCPToolsets: map[string]googleadk.MCPFactory{"guarded-server": guardedMCPFactory()},
	})
	return env, counter
}

// TestMCPToolConfirmationPausesAndResumes proves ADK's standard worker-side
// tool-confirmation protocol tunnels across the Activity boundary: the guarded
// MCP tool's confirmation request pauses the agent (PendingConfirmations
// surfaces it, with the original call attached for the human), and the approval
// flows back through the snapshot so the tool executes exactly once.
func TestMCPToolConfirmationPausesAndResumes(t *testing.T) {
	mcpGuardedRuns.Store(0)

	var s testsuite.WorkflowTestSuite
	env, counter := newMcpHitlEnv(t, &s)
	env.ExecuteWorkflow(mcpHitlWorkflow, true)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res mcpHitlResult
	require.NoError(t, env.GetWorkflowResult(&res))

	require.True(t, res.Paused, "agent must pause on a pending confirmation")
	assert.Equal(t, "guarded", res.OriginalCallName, "the human must see the original tool call")
	assert.NotEmpty(t, res.PendingHint)
	assert.EqualValues(t, 1, mcpGuardedRuns.Load(), "guarded MCP tool must run exactly once, only after approval")
	assert.Contains(t, res.ResumedToolResponses, "guarded")
	assert.Equal(t, 2, counter.get(googleadk.CallMcpToolActivityName),
		"one pending pass, one approved execution")
}

// TestMCPToolConfirmationDenialBlocksTool proves the denial path: the human's
// rejection crosses the Activity boundary to the worker-side tool, which
// refuses without ever running the guarded handler, and the rejection surfaces
// in the tool's function response.
func TestMCPToolConfirmationDenialBlocksTool(t *testing.T) {
	mcpGuardedRuns.Store(0)

	var s testsuite.WorkflowTestSuite
	env, counter := newMcpHitlEnv(t, &s)
	env.ExecuteWorkflow(mcpHitlWorkflow, false)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res mcpHitlResult
	require.NoError(t, env.GetWorkflowResult(&res))

	require.True(t, res.Paused, "agent must pause on a pending confirmation")
	assert.EqualValues(t, 0, mcpGuardedRuns.Load(), "denied tool must never execute")
	assert.Contains(t, res.ResumedGuardedError, "rejected",
		"the resumed function response must carry the rejection")
	assert.Equal(t, 2, counter.get(googleadk.CallMcpToolActivityName),
		"the denial is delivered to the worker-side tool, which rejects without running the handler")
}
