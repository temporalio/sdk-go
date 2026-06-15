package google_adk_agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/adk/session"
)

// stubEvent builds a model-authored event carrying text, mimicking what a real
// ADK turn appends to the session.
func stubEvent(text string) *session.Event {
	ev := session.NewEvent("inv")
	ev.Author = "stub"
	ev.Content = modelMessage(text)
	return ev
}

// replyTurn returns a stub RunTurnActivity that echoes a fixed reply, appending
// to the prior events it receives so the durable transcript grows turn over
// turn (exactly as the real activity folds events into the snapshot). It lets
// the workflow tests assert orchestration behavior without a live model.
func replyTurn(reply string) func(context.Context, adk.TurnInput) (adk.TurnResult, error) {
	return func(_ context.Context, in adk.TurnInput) (adk.TurnResult, error) {
		events := append(append([]*session.Event{}, in.PriorEvents...), stubEvent(reply))
		return adk.TurnResult{
			Events:       events,
			SessionState: map[string]any{"turns": len(events)},
		}, nil
	}
}

// queryConversation reads the transcript via the conversation query at a fixed
// virtual time, after the in-flight turn(s) have completed. The session
// workflow never completes on its own, so querying during execution (rather
// than after) is the correct pattern.
func queryConversation(t *testing.T, env *testsuite.TestWorkflowEnvironment, out *[]*session.Event) {
	val, err := env.QueryWorkflow(adk.QueryConversation)
	require.NoError(t, err)
	require.NoError(t, val.Get(out))
}

// TestSendMessageSignalRunsTurn: a SendMessage signal drives exactly one turn
// and the reply lands in the durable transcript.
func TestSendMessageSignalRunsTurn(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("pong"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("ping")})
	}, 0)

	var events []*session.Event
	env.RegisterDelayedCallback(func() { queryConversation(t, env, &events) }, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.True(t, containsText(events, "pong"), "the turn reply must be in the transcript")
}

// TestConversationQuery: multiple turns accumulate in order in the transcript
// returned by the conversation query.
func TestConversationQuery(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("ack"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("first")})
	}, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("second")})
	}, time.Second)

	var events []*session.Event
	env.RegisterDelayedCallback(func() { queryConversation(t, env, &events) }, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	// Two turns, each appending one model event onto the growing snapshot.
	require.Len(t, events, 2, "both turns should be recorded")
}

// TestTurnResultEventsPersisted: the exact events a turn Activity returns are
// what the conversation query surfaces — the workflow persists TurnResult.Events
// verbatim.
func TestTurnResultEventsPersisted(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(
		func(_ context.Context, _ adk.TurnInput) (adk.TurnResult, error) {
			return adk.TurnResult{Events: []*session.Event{stubEvent("only-event")}}, nil
		},
		activity.RegisterOptions{Name: adk.ActivityNameRunTurn},
	)

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("go")})
	}, 0)

	var events []*session.Event
	env.RegisterDelayedCallback(func() { queryConversation(t, env, &events) }, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.Len(t, events, 1)
	require.True(t, containsText(events, "only-event"))
}
