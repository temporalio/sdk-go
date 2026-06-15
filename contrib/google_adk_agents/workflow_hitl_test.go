package google_adk_agents_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/adk/session"
)

// confirmTurn returns a stub turn Activity that emulates ADK's tool-confirmation
// pause/resume: the first turn returns a pending confirmation (and no resolved
// reply); every later turn (the confirmation function-response having arrived)
// returns the final reply with no pending confirmations.
func confirmTurn(reply string) func(context.Context, adk.TurnInput) (adk.TurnResult, error) {
	var calls int32
	return func(_ context.Context, in adk.TurnInput) (adk.TurnResult, error) {
		events := append(append([]*session.Event{}, in.PriorEvents...), stubEvent("working"))
		if atomic.AddInt32(&calls, 1) == 1 {
			return adk.TurnResult{
				Events: events,
				PendingConfirmations: []adk.ConfirmationRequest{{
					FunctionCallID: "fc-1",
					ToolName:       "delete_database",
					Hint:           "Approve dropping the production database?",
					Payload:        map[string]any{"table": "prod"},
				}},
			}, nil
		}
		return adk.TurnResult{Events: append(events, stubEvent(reply))}, nil
	}
}

// TestPendingConfirmationsQuery: after a turn raises a tool confirmation, the
// pending_confirmations query surfaces the full ConfirmationRequest fields so a
// UI can render the prompt.
func TestPendingConfirmationsQuery(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(confirmTurn("approved"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "ops", "ops", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("drop prod")})
	}, 0)

	var pending []adk.ConfirmationRequest
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(adk.QueryPendingConfirmations)
		require.NoError(t, err)
		require.NoError(t, val.Get(&pending))
	}, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.Len(t, pending, 1)
	require.Equal(t, "fc-1", pending[0].FunctionCallID)
	require.Equal(t, "delete_database", pending[0].ToolName)
	require.NotEmpty(t, pending[0].Hint)
}

// TestToolConfirmationResume: a confirm Signal resolves the pending confirmation
// and ADK's native resume path runs the next turn, clearing the confirmation and
// producing the final reply.
func TestToolConfirmationResume(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(confirmTurn("deleted"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "ops", "ops", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("drop prod")})
	}, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalConfirm, adk.ToolConfirmationDecision{
			FunctionCallID: "fc-1",
			ToolName:       "delete_database",
			Confirmed:      true,
		})
	}, time.Second)

	var pending []adk.ConfirmationRequest
	var events []*session.Event
	env.RegisterDelayedCallback(func() {
		pv, err := env.QueryWorkflow(adk.QueryPendingConfirmations)
		require.NoError(t, err)
		require.NoError(t, pv.Get(&pending))
		queryConversation(t, env, &events)
	}, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.Empty(t, pending, "the confirmation must be cleared after resume")
	require.True(t, containsText(events, "deleted"), "the resumed turn reply must be recorded")
}

// TestToolConfirmationResumeViaUpdate: the Confirm update resolves the pending
// confirmation and returns the resumed TurnResult, and its validator rejects a
// decision missing a FunctionCallID.
func TestToolConfirmationResumeViaUpdate(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(confirmTurn("deleted"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "ops", "ops", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("drop prod")})
	}, 0)

	// Validator rejects a decision with no FunctionCallID.
	var rejectErr error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(adk.UpdateConfirm, "confirm-bad", &testsuite.TestUpdateCallback{
			OnAccept:   func() { t.Error("validator must reject a decision missing FunctionCallID") },
			OnReject:   func(err error) { rejectErr = err },
			OnComplete: func(interface{}, error) {},
		}, adk.ToolConfirmationDecision{Confirmed: true})
	}, time.Second)

	// Valid confirmation resumes and returns the resulting TurnResult.
	var got adk.TurnResult
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(adk.UpdateConfirm, "confirm-ok", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) { t.Errorf("valid confirmation must not be rejected: %v", err) },
			OnComplete: func(result interface{}, err error) {
				require.NoError(t, err)
				res, ok := result.(adk.TurnResult)
				require.True(t, ok)
				got = res
			},
		}, adk.ToolConfirmationDecision{FunctionCallID: "fc-1", ToolName: "delete_database", Confirmed: true})
	}, 2*time.Second)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.Error(t, rejectErr, "missing FunctionCallID must be rejected")
	require.True(t, containsText(got.Events, "deleted"))
}
