package googleadk_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
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
	// ResumedToolResponses are the tool response names seen on the resume pass (the
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

// ----------------------------------------------------------------------------
// Multi-decision batched resume.
// ----------------------------------------------------------------------------

// guardedAlphaRuns / guardedBetaRuns / guardedGammaRuns count executions of
// the three guarded Activities. They stay at zero while the agent is paused
// awaiting the batched confirmation.
var (
	guardedAlphaRuns atomic.Int64
	guardedBetaRuns  atomic.Int64
	guardedGammaRuns atomic.Int64
)

// guardedTrioArgs is the (empty) argument struct for the guarded trio
// activities; ActivityAsTool infers the tool schema from it.
type guardedTrioArgs struct{}

// guardedAlphaActivity / guardedBetaActivity / guardedGammaActivity are three
// distinct Temporal activities exposed as guarded ActivityAsTool tools, so a
// single approved resume dispatches three independent Activities.
func guardedAlphaActivity(context.Context, guardedTrioArgs) (map[string]any, error) {
	guardedAlphaRuns.Add(1)
	return map[string]any{"status": "alpha executed"}, nil
}

func guardedBetaActivity(context.Context, guardedTrioArgs) (map[string]any, error) {
	guardedBetaRuns.Add(1)
	return map[string]any{"status": "beta executed"}, nil
}

func guardedGammaActivity(context.Context, guardedTrioArgs) (map[string]any, error) {
	guardedGammaRuns.Add(1)
	return map[string]any{"status": "gamma executed"}, nil
}

// staticToolset is a minimal tool.Toolset over a fixed tool list, so the test
// can wrap ActivityAsTool tools with ADK's own standard confirmation guard
// (tool.WithConfirmation) — the identical logic mcptoolset applies under its
// RequireConfirmation option.
type staticToolset struct {
	name  string
	tools []tool.Tool
}

func (s staticToolset) Name() string                                     { return s.name }
func (s staticToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) { return s.tools, nil }

// guardedTrioCalls is the scripted model turn that calls all THREE guarded
// tools in one turn — request order alpha, beta, gamma — so all three pause on
// a confirmation in the same pass: the shape whose batched resume used to be
// replay-hazardous before upstream re-queued approved calls in request order.
func guardedTrioCalls() *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "call-alpha", Name: "guarded_alpha", Args: map[string]any{}}},
				{FunctionCall: &genai.FunctionCall{ID: "call-beta", Name: "guarded_beta", Args: map[string]any{}}},
				{FunctionCall: &genai.FunctionCall{ID: "call-gamma", Name: "guarded_gamma", Args: map[string]any{}}},
			},
		},
	}
}

// multiConfirmResult is the serializable output of multiConfirmHitlWorkflow.
type multiConfirmResult struct {
	// PendingCount is the number of confirmations the first Run pass paused on.
	PendingCount int
	// PendingCalls are the original tool-call names awaiting approval, in the
	// order PendingConfirmations surfaced them.
	PendingCalls []string
	// ResumedTexts are the model texts produced after the batched approval.
	ResumedTexts []string
	// ResumedToolResponses are the tool response names seen on the resume pass,
	// in event/part order — the ordered record the tests assert the request-order
	// contract against.
	ResumedToolResponses []string
}

// multiConfirmHitlWorkflow drives the multi-decision HITL cycle in-workflow,
// mirroring hitlWorkflow's two-pass loop over one shared session:
//
//	Pass 1: one model turn calls THREE guarded ActivityAsTool tools (request
//	        order alpha, beta, gamma); each pauses on a confirmation, so
//	        PendingConfirmations surfaces three pending decisions from the
//	        same pass.
//	Pass 2: the workflow approves all three in a single batched
//	        ConfirmationResponse whose decisions are deliberately ROTATED to
//	        (gamma, alpha, beta). ADK re-queues the approved calls in the
//	        request order of their confirmations, dispatching the three
//	        underlying Temporal Activities in one resume pass.
//
// The rotation makes the strict order assertions in the tests meaningful:
// decision order matches neither the request order (alpha, beta, gamma) nor
// its reverse (gamma, beta, alpha), so a re-dispatch keyed on decision
// position — or on reversed request order — produces a detectably different
// response order. The replay integration test replays this workflow's
// recorded history to prove the batched resume is deterministic.
func multiConfirmHitlWorkflow(ctx workflow.Context) (multiConfirmResult, error) {
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	alpha, err := googleadk.ActivityAsTool(guardedAlphaActivity, googleadk.ActivityToolOptions{
		Name: "guarded_alpha", Description: "guarded tool alpha", ActivityOptions: ao,
	})
	if err != nil {
		return multiConfirmResult{}, err
	}
	beta, err := googleadk.ActivityAsTool(guardedBetaActivity, googleadk.ActivityToolOptions{
		Name: "guarded_beta", Description: "guarded tool beta", ActivityOptions: ao,
	})
	if err != nil {
		return multiConfirmResult{}, err
	}
	gamma, err := googleadk.ActivityAsTool(guardedGammaActivity, googleadk.ActivityToolOptions{
		Name: "guarded_gamma", Description: "guarded tool gamma", ActivityOptions: ao,
	})
	if err != nil {
		return multiConfirmResult{}, err
	}
	guarded := tool.WithConfirmation(staticToolset{name: "guarded-trio", tools: []tool.Tool{alpha, beta, gamma}}, true, nil)

	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "root assistant",
		Model:       googleadk.NewModel("multi-confirm-model"),
		Instruction: "be helpful",
		Toolsets:    []tool.Toolset{guarded},
	})
	if err != nil {
		return multiConfirmResult{}, err
	}
	r, err := runner.New(runner.Config{
		AppName:           "test-app",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return multiConfirmResult{}, err
	}

	adkCtx := googleadk.NewContext(ctx)

	// --- Pass 1: run until the agent pauses on all three confirmations. ---
	var pass1 []*session.Event
	msg := genai.NewContentFromText("run all three guarded tools", genai.RoleUser)
	for ev, rerr := range r.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return multiConfirmResult{}, rerr
		}
		if ev != nil {
			pass1 = append(pass1, ev)
		}
	}

	pending := googleadk.PendingConfirmations(pass1)
	var res multiConfirmResult
	res.PendingCount = len(pending)
	confirmationIDs := make(map[string]string, len(pending)) // original tool name -> confirmation FunctionCall ID
	for _, p := range pending {
		if p.OriginalCall != nil {
			res.PendingCalls = append(res.PendingCalls, p.OriginalCall.Name)
			confirmationIDs[p.OriginalCall.Name] = p.FunctionCallID
		}
	}
	if len(confirmationIDs) < 3 {
		return res, nil // Not fully paused; the test will fail on res.PendingCount.
	}

	// --- Pass 2: approve all three decisions in one batched resume, ROTATED to
	// (gamma, alpha, beta) — neither the request order nor its reverse. ---
	resume := googleadk.ConfirmationResponse(
		googleadk.ConfirmationDecision{FunctionCallID: confirmationIDs["guarded_gamma"], Confirmed: true},
		googleadk.ConfirmationDecision{FunctionCallID: confirmationIDs["guarded_alpha"], Confirmed: true},
		googleadk.ConfirmationDecision{FunctionCallID: confirmationIDs["guarded_beta"], Confirmed: true},
	)
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
			if p.FunctionResponse != nil {
				res.ResumedToolResponses = append(res.ResumedToolResponses, p.FunctionResponse.Name)
			}
		}
	}
	return res, nil
}

// registerGuardedTrio registers multiConfirmHitlWorkflow's three guarded
// activities under the tool names their ActivityAsTool wrappers dispatch.
func registerGuardedTrio(r interface {
	RegisterActivityWithOptions(any, activity.RegisterOptions)
}) {
	r.RegisterActivityWithOptions(guardedAlphaActivity, activity.RegisterOptions{Name: "guarded_alpha"})
	r.RegisterActivityWithOptions(guardedBetaActivity, activity.RegisterOptions{Name: "guarded_beta"})
	r.RegisterActivityWithOptions(guardedGammaActivity, activity.RegisterOptions{Name: "guarded_gamma"})
}

// multiConfirmModels is the scripted model config for multiConfirmHitlWorkflow:
// one turn calling all three guarded tools, then a final text once all execute.
func multiConfirmModels() map[string]googleadk.ModelFactory {
	return map[string]googleadk.ModelFactory{
		"multi-confirm-model": scriptedModelFactory(guardedTrioCalls(), googleadk.TextResponse("all three done")),
	}
}

// TestHITLMultiDecisionBatchedResume pins the multi-decision confirmation
// contract: when one model turn pauses on THREE guarded ActivityAsTool tools,
// all decisions can be resumed in a single ConfirmationResponse, and the
// resumed batch's tool responses — and with them its Activity scheduling —
// follow the confirmations' request order (alpha, beta, gamma) regardless of
// where each decision sits in the resume message (deliberately rotated to
// gamma, alpha, beta). The strict order assertion catches any deterministic
// wrong-order regression, such as a re-dispatch keyed on decision order. It
// does NOT probabilistically re-prove upstream's map-iteration fix: Go's
// small-map iteration is insertion-biased, so a map-order regression could
// still pass an individual run here — upstream pins that with its own
// TestRequestConfirmationResumeOrderIsStable (google/adk-go#1169). The replay
// scenario in TestReplaySingleAndMultiAgent guards the Temporal-side
// determinism contract.
func TestHITLMultiDecisionBatchedResume(t *testing.T) {
	guardedAlphaRuns.Store(0)
	guardedBetaRuns.Store(0)
	guardedGammaRuns.Store(0)

	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(multiConfirmHitlWorkflow)
	registerGuardedTrio(env)
	counter := wireActivities(t, env, googleadk.Config{Models: multiConfirmModels()})

	env.ExecuteWorkflow(multiConfirmHitlWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res multiConfirmResult
	require.NoError(t, env.GetWorkflowResult(&res))

	require.Equal(t, 3, res.PendingCount, "all three guarded tools must pause in the same pass")
	assert.Equal(t, []string{"guarded_alpha", "guarded_beta", "guarded_gamma"}, res.PendingCalls,
		"the pending confirmations must surface in request order")
	assert.EqualValues(t, 1, guardedAlphaRuns.Load(), "alpha must run exactly once, only after approval")
	assert.EqualValues(t, 1, guardedBetaRuns.Load(), "beta must run exactly once, only after approval")
	assert.EqualValues(t, 1, guardedGammaRuns.Load(), "gamma must run exactly once, only after approval")
	assert.Equal(t, 1, counter.get("guarded_alpha"), "alpha must be dispatched as a Temporal Activity")
	assert.Equal(t, 1, counter.get("guarded_beta"), "beta must be dispatched as a Temporal Activity")
	assert.Equal(t, 1, counter.get("guarded_gamma"), "gamma must be dispatched as a Temporal Activity")
	assert.Equal(t, []string{"guarded_alpha", "guarded_beta", "guarded_gamma"}, res.ResumedToolResponses,
		"resumed responses must follow the confirmations' request order, not the rotated decision order")
	assert.Contains(t, res.ResumedTexts, "all three done")
}
