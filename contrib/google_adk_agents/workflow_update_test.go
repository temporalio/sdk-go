package google_adk_agents_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
)

// TestSendMessageAndWaitUpdate: the SendMessageAndWait update runs a turn and
// returns its TurnResult to the caller synchronously. In the test environment
// the update result is the raw Go value, so it type-asserts to adk.TurnResult.
func TestSendMessageAndWaitUpdate(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("done"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")

	var got adk.TurnResult
	var completeErr error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(adk.UpdateSendMessageAndWait, "upd-1", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) { completeErr = err },
			OnComplete: func(result interface{}, err error) {
				if err != nil {
					completeErr = err
					return
				}
				res, ok := result.(adk.TurnResult)
				require.True(t, ok, "update result should be a TurnResult, got %T", result)
				got = res
			},
		}, adk.TurnRequest{Message: userMessage("hi")})
	}, 0)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.NoError(t, completeErr)
	require.True(t, containsText(got.Events, "done"), "the awaited turn result must carry the reply")
}

// TestSendMessageAndWaitValidatorRejects: the update validator rejects an empty
// message at the workflow boundary, before any turn Activity is scheduled.
func TestSendMessageAndWaitValidatorRejects(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(replyTurn("unreached"), activity.RegisterOptions{Name: adk.ActivityNameRunTurn})

	in := adk.NewAgentSessionInput(adk.Options{}, "chat", "chat", "u", "s")

	var rejectErr error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(adk.UpdateSendMessageAndWait, "upd-empty", &testsuite.TestUpdateCallback{
			OnAccept:   func() { t.Error("validator must reject an empty message before acceptance") },
			OnReject:   func(err error) { rejectErr = err },
			OnComplete: func(interface{}, error) {},
		}, adk.TurnRequest{Message: nil})
	}, 0)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)
	require.Error(t, rejectErr, "empty message must be rejected by the validator")
}
