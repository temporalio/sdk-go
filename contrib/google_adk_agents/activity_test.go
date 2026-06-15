package google_adk_agents_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
)

// TestTurnEventIdentityReproducible runs the same turn twice with an identical
// DeterminismSeed (as a retry would) and asserts the event IDs and timestamps
// are byte-for-byte reproducible — the guarantee WithDeterministicProviders
// gives across Activity retries and continue-as-new.
func TestTurnEventIdentityReproducible(t *testing.T) {
	m := adk.NewMockModel("det-model", modelMessage("deterministic reply"))
	reg := adk.NewAgentRegistry()
	reg.Register("det", newAgentFactory("det", m))
	acts := adk.NewActivities(reg, adk.Options{})

	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	seed := adk.DeterminismSeed{BaseTime: time.Unix(1_700_000_000, 0).UTC(), UUIDSeed: "fixed-seed"}
	run := func() adk.TurnResult {
		val, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
			AgentName: "det", AppName: "det", UserID: "u", SessionID: "s",
			Message: userMessage("same input"),
			Seed:    seed,
		})
		require.NoError(t, err)
		var res adk.TurnResult
		require.NoError(t, val.Get(&res))
		return res
	}

	first, second := run(), run()
	require.NotEmpty(t, first.Events)
	require.Equal(t, len(first.Events), len(second.Events))
	for i := range first.Events {
		require.Equal(t, first.Events[i].ID, second.Events[i].ID, "event %d ID must be reproducible", i)
		require.Equal(t, first.Events[i].Timestamp, second.Events[i].Timestamp, "event %d timestamp must be reproducible", i)
	}
}

// TestActivitySummarySet verifies the exported Summary helper RunTurn uses to
// label each turn Activity: a custom SummaryFunc wins, and the agent name is
// the default.
func TestActivitySummarySet(t *testing.T) {
	in := adk.TurnInput{AgentName: "researcher"}

	custom := adk.ActivityOptions{SummaryFunc: func(ti adk.TurnInput) string {
		return "turn:" + ti.AgentName
	}}
	require.Equal(t, "turn:researcher", custom.Summary(in))

	require.Equal(t, "researcher", adk.ActivityOptions{}.Summary(in), "summary defaults to the agent name")
}

// TestPerAgentTimeout proves a per-agent StartToCloseTimeout override flows
// through NewAgentSessionInput into the serializable TurnSettings, while other
// agents fall back to the default — a config field that changes behavior.
func TestPerAgentTimeout(t *testing.T) {
	opts := adk.Options{
		DefaultActivityOptions: adk.ActivityOptions{StartToCloseTimeout: time.Minute},
		ActivityOptions: map[string]adk.ActivityOptions{
			"thinker": {StartToCloseTimeout: time.Hour},
		},
	}

	slow := adk.NewAgentSessionInput(opts, "thinker", "thinker", "u", "s")
	require.Equal(t, time.Hour, slow.Settings.StartToCloseTimeout)

	fast := adk.NewAgentSessionInput(opts, "chatbot", "chatbot", "u", "s")
	require.Equal(t, time.Minute, fast.Settings.StartToCloseTimeout)
}
