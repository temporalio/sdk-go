package google_adk_agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/adk/session"
)

// decodeContinueAsNew extracts the AgentSessionInput carried into the next run
// of a continue-as-new boundary, decoding the recorded Input payloads with the
// default converter the test environment used to encode them.
func decodeContinueAsNew(t *testing.T, err error) adk.AgentSessionInput {
	t.Helper()
	var canErr *workflow.ContinueAsNewError
	require.True(t, errors.As(err, &canErr), "workflow should end with a ContinueAsNewError, got %v", err)
	var resumed adk.AgentSessionInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(canErr.Input, &resumed))
	return resumed
}

// TestContinueAsNewMaxTurns: crossing CANThreshold.MaxTurns triggers a
// continue-as-new at a clean boundary.
func TestContinueAsNewMaxTurns(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("ack"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	opts := adk.Options{ContinueAsNewThreshold: adk.CANThreshold{MaxTurns: 1}}
	in := adk.NewAgentSessionInput(opts, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("only turn")})
	}, 0)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.True(t, env.IsWorkflowCompleted())

	resumed := decodeContinueAsNew(t, env.GetWorkflowError())
	require.NotNil(t, resumed.ResumeState, "continue-as-new must carry a ResumeState")
	require.Equal(t, 1, resumed.ResumeState.TurnCount, "turn counter must carry across the boundary")
}

// TestContinueAsNewMaxBytes: crossing CANThreshold.MaxSessionBytes triggers a
// continue-as-new even before the turn cap is reached.
func TestContinueAsNewMaxBytes(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("ack"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	// One event marshals to far more than one byte, so a single turn crosses it.
	opts := adk.Options{ContinueAsNewThreshold: adk.CANThreshold{MaxSessionBytes: 1}}
	in := adk.NewAgentSessionInput(opts, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("bytes turn")})
	}, 0)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.True(t, env.IsWorkflowCompleted())

	resumed := decodeContinueAsNew(t, env.GetWorkflowError())
	require.NotNil(t, resumed.ResumeState)
	require.GreaterOrEqual(t, len(resumed.ResumeState.PriorEvents), 1, "the session snapshot must carry forward")
}

// TestSessionStateCarried: the transcript and ADK session state survive the
// continue-as-new boundary so the resumed run sees the full conversation.
func TestSessionStateCarried(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(
		func(_ context.Context, in adk.TurnInput) (adk.TurnResult, error) {
			events := append(append([]*session.Event{}, in.PriorEvents...), stubEvent("reply"))
			return adk.TurnResult{Events: events, SessionState: map[string]any{"carried": "yes"}}, nil
		},
		activity.RegisterOptions{Name: adk.ActivityNameRunTurn},
	)

	opts := adk.Options{ContinueAsNewThreshold: adk.CANThreshold{MaxTurns: 2}}
	in := adk.NewAgentSessionInput(opts, "chat", "chat", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("first")})
	}, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("second")})
	}, time.Second)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.True(t, env.IsWorkflowCompleted())

	resumed := decodeContinueAsNew(t, env.GetWorkflowError())
	require.NotNil(t, resumed.ResumeState)
	require.Equal(t, 2, resumed.ResumeState.TurnCount)
	require.Len(t, resumed.ResumeState.PriorEvents, 2, "both turns' events carry forward")
	require.Equal(t, "yes", resumed.ResumeState.SessionState["carried"], "ADK session state carries forward")
}
