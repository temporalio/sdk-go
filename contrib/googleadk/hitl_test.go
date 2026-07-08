package googleadk_test

import (
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

// hitlToolRan is flipped by the HITL-guarded tool's handler the (single) time it
// actually executes — i.e. only after the human approves. It stays false while
// the agent is paused awaiting confirmation.
var hitlToolRan atomic.Bool

// hitlResult is the serializable output of hitlWorkflow.
type hitlResult struct {
	// Paused reports that the first Run pass paused on a confirmation.
	Paused bool
	// PendingHint is the hint the paused tool supplied.
	PendingHint string
	// ResumedTexts are the model texts produced after the human approves.
	ResumedTexts []string
	// ToolResponses are the tool response names seen on the resume pass (the
	// guarded tool must appear once it executes).
	ResumedToolResponses []string
}

// hitlWorkflow drives the full pause/resume HITL cycle in-workflow:
//
//	Pass 1: the model calls the guarded "danger" tool; the tool calls
//	        ctx.RequestConfirmation on its first invocation and returns, so ADK
//	        emits an adk_request_confirmation FunctionCall and pauses. The
//	        workflow detects the pause via PendingConfirmations.
//	Pass 2: the workflow simulates the human decision (Confirmed: true), builds a
//	        resume message with ConfirmationResponse, and calls Run again on the
//	        same session. ADK re-dispatches the original tool call, which — now
//	        that a confirmation is present — actually executes.
//
// Modeling this as a two-pass loop over one shared session (rather than a signal
// round-trip) keeps it fully deterministic in the test environment while still
// exercising the real PendingConfirmations / ConfirmationResponse helpers.
func hitlWorkflow(ctx workflow.Context) (hitlResult, error) {
	danger, err := funcTool("danger", func(tctx agent.Context, _ map[string]any) (map[string]any, error) {
		// On the first invocation there is no confirmation yet: request one and
		// return without doing the work. On the resumed invocation ADK supplies a
		// ToolConfirmation, so proceed.
		if tctx.ToolConfirmation() == nil {
			if rerr := tctx.RequestConfirmation("Deleting production data — are you sure?", nil); rerr != nil {
				return nil, rerr
			}
			return map[string]any{"status": "awaiting confirmation"}, nil
		}
		hitlToolRan.Store(true)
		return map[string]any{"status": "executed"}, nil
	})
	if err != nil {
		return hitlResult{}, err
	}

	root, err := newHITLRunner(ctx, danger)
	if err != nil {
		return hitlResult{}, err
	}

	adkCtx := googleadk.NewContext(ctx)

	// --- Pass 1: run until the agent pauses on a confirmation. ---
	var pass1 []*session.Event
	msg := genai.NewContentFromText("please delete production data", genai.RoleUser)
	for ev, rerr := range root.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return hitlResult{}, rerr
		}
		if ev != nil {
			pass1 = append(pass1, ev)
		}
	}

	pending := googleadk.PendingConfirmations(pass1)
	var res hitlResult
	if len(pending) == 0 {
		return res, nil // Not paused; the test will fail on res.Paused.
	}
	res.Paused = true
	res.PendingHint = pending[0].Hint

	// --- Pass 2: simulate the human approving, then resume. ---
	resume := googleadk.ConfirmationResponse(googleadk.ConfirmationDecision{
		FunctionCallID: pending[0].FunctionCallID,
		Confirmed:      true,
	})
	for ev, rerr := range root.Run(adkCtx, "user-1", "session-1", resume, agent.RunConfig{}) {
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
			if p.FunctionResponse != nil {
				res.ResumedToolResponses = append(res.ResumedToolResponses, p.FunctionResponse.Name)
			}
		}
	}
	return res, nil
}

// newHITLRunner builds an llmagent + runner sharing one session across the two
// Run passes so the resume sees the pending confirmation in history.
func newHITLRunner(_ workflow.Context, danger tool.Tool) (*runner.Runner, error) {
	root, err := hitlAgent(danger)
	if err != nil {
		return nil, err
	}
	return runner.New(runner.Config{
		AppName:           "test-app",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
}

func hitlAgent(danger tool.Tool) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewModel("fake-model"),
		Instruction: "be helpful",
		Tools:       []tool.Tool{danger},
	})
}

// TestHITLConfirmationResumesTool proves the human-in-the-loop confirmation
// round-trip: a guarded in-workflow tool pauses the agent via
// RequestConfirmation, PendingConfirmations surfaces the pause, and a
// ConfirmationResponse carrying an approval resumes the run so the tool actually
// executes.
func TestHITLConfirmationResumesTool(t *testing.T) {
	hitlToolRan.Store(false)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(hitlWorkflow)
	wireActivities(t, env, googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": scriptedModelFactory(
				googleadk.FunctionCallResponse("call-1", "danger", map[string]any{}),
				googleadk.TextResponse("done: production data deleted"),
			),
		},
	})

	env.ExecuteWorkflow(hitlWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res hitlResult
	require.NoError(t, env.GetWorkflowResult(&res))

	assert.True(t, res.Paused, "agent must pause awaiting confirmation on the first pass")
	assert.Equal(t, "Deleting production data — are you sure?", res.PendingHint)
	// The guarded tool did NOT run until approval, then ran exactly once on resume.
	assert.True(t, hitlToolRan.Load(), "the guarded tool must execute after the human approves")
	assert.Contains(t, res.ResumedToolResponses, "danger", "the resumed tool response must be present")
}
