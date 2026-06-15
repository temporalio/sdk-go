package google_adk_agents_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/adk/session"
)

// TestNativeAgentTurnThroughActivity is the cardinal native-integration test:
// it imports adk-go directly, builds a real llmagent + runner in a factory, and
// drives one full turn through RunTurnActivity in the Temporal test
// environment. No network and no env-var gate — it runs by default.
func TestNativeAgentTurnThroughActivity(t *testing.T) {
	m := adk.NewMockModel("greeter-model", modelMessage("hi there"))
	reg := adk.NewAgentRegistry()
	reg.Register("greeter", newAgentFactory("greeter", m))

	acts := adk.NewActivities(reg, adk.Options{})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	val, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
		AgentName: "greeter",
		AppName:   "greeter",
		UserID:    "u1",
		SessionID: "s1",
		Message:   userMessage("hello"),
		Seed:      adk.DeterminismSeed{BaseTime: time.Unix(1_700_000_000, 0).UTC(), UUIDSeed: "seed"},
	})
	require.NoError(t, err)

	var res adk.TurnResult
	require.NoError(t, val.Get(&res))
	require.NotEmpty(t, res.Events, "turn should append at least the model event")
	require.True(t, containsText(res.Events, "hi there"), "model reply should be in the snapshot")
}

// TestMultipleRegisteredAgents proves the registry hosts several named agents
// on one worker and routes each turn to the right factory.
func TestMultipleRegisteredAgents(t *testing.T) {
	reg := adk.NewAgentRegistry()
	reg.Register("english", newAgentFactory("english", adk.NewMockModel("en", modelMessage("hello"))))
	reg.Register("french", newAgentFactory("french", adk.NewMockModel("fr", modelMessage("bonjour"))))

	acts := adk.NewActivities(reg, adk.Options{})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	for name, want := range map[string]string{"english": "hello", "french": "bonjour"} {
		val, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
			AgentName: name, AppName: name, UserID: "u", SessionID: "s",
			Message: userMessage("hi"),
		})
		require.NoError(t, err)
		var res adk.TurnResult
		require.NoError(t, val.Get(&res))
		require.True(t, containsText(res.Events, want), "agent %q should reply %q", name, want)
	}
}

// TestNativeAgentTurnThroughWorkflow is an end-to-end test through the durable
// workflow: a real adk-go runner executes inside the real RunTurnActivity,
// driven by AgentSessionWorkflow via a SendMessage signal, with the transcript
// read back through the conversation query. Runs by default, no dev server.
func TestNativeAgentTurnThroughWorkflow(t *testing.T) {
	m := adk.NewMockModel("chat-model", modelMessage("pong"))
	reg := adk.NewAgentRegistry()
	reg.Register("chat", newAgentFactory("chat", m))
	acts := adk.NewActivities(reg, adk.Options{})

	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})
	env.RegisterActivityWithOptions(acts.RunTurnStreamingActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurnStreaming})

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u1", "s1")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("ping")})
	}, 0)

	// The session workflow loops forever awaiting more turns, so the auto-time-
	// skipping test env eventually times it out; query the transcript while it
	// is still live (after the turn has run) via a delayed callback.
	var events []*session.Event
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(adk.QueryConversation)
		require.NoError(t, err)
		require.NoError(t, val.Get(&events))
	}, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)

	require.True(t, containsText(events, "pong"), "conversation query should show the model reply")
}
