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

// TestSSEStreamingSurfacesPartials: partial StreamChunks delivered to the
// workflow on the streaming signal (as RunTurnStreamingActivity emits them)
// accumulate in order and are observable through the pending_stream query while
// the turn is still in flight.
func TestSSEStreamingSurfacesPartials(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.DefaultStreamingSignalName, adk.StreamChunk{Index: 0, Text: "Hel"})
		env.SignalWorkflow(adk.DefaultStreamingSignalName, adk.StreamChunk{Index: 1, Text: "lo, "})
		env.SignalWorkflow(adk.DefaultStreamingSignalName, adk.StreamChunk{Index: 2, Text: "world", Done: true})
	}, 0)

	var chunks []adk.StreamChunk
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(adk.QueryPendingStream)
		require.NoError(t, err)
		require.NoError(t, val.Get(&chunks))
	}, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)

	require.Len(t, chunks, 3)
	var combined string
	for i, c := range chunks {
		require.Equal(t, i, c.Index, "chunk index must be preserved in order")
		combined += c.Text
	}
	require.Equal(t, "Hello, world", combined)
	require.True(t, chunks[2].Done, "final chunk marks the stream complete")
}

// TestStreamingRoutesToStreamingActivity: flipping Settings.Streaming to true
// routes turns to RunTurnStreamingActivity instead of RunTurnActivity — a config
// field that changes observable dispatch. Both activities are registered with
// distinguishable replies; the streaming reply proves the route.
func TestStreamingRoutesToStreamingActivity(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("non-streaming"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})
	env.RegisterActivityWithOptions(replyTurn("streaming"), activity.RegisterOptions{Name: adk.ActivityNameRunTurnStreaming})

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")
	in.Settings.Streaming = true

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("ping")})
	}, 0)

	var events []*session.Event
	env.RegisterDelayedCallback(func() { queryConversation(t, env, &events) }, time.Minute)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.True(t, containsText(events, "streaming"), "Streaming=true must route to RunTurnStreamingActivity")
	require.False(t, containsText(events, "non-streaming"))
}
