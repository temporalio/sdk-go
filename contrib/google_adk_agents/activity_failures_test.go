package google_adk_agents_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// appErrorType extracts the classified ApplicationError Type from an
// activity/workflow error, asserting the chain actually carries one.
// Classification is never done by string-matching err.Error(). Both the turn
// Activity and the session workflow surface the classified type as the
// outermost ApplicationError, so a single errors.As resolves it.
func appErrorType(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "error must be an ApplicationError: %v", err)
	return appErr.Type()
}

func newActivityEnv(t *testing.T, reg *adk.AgentRegistry) *testsuite.TestActivityEnvironment {
	t.Helper()
	acts := adk.NewActivities(reg, adk.Options{})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(acts.RunTurnActivity, activity.RegisterOptions{Name: adk.ActivityNameRunTurn})
	return env
}

// TestNonRetryableUnknownAgent: dispatching an unregistered agent is a
// configuration error classified ADKNonRetryable.
func TestNonRetryableUnknownAgent(t *testing.T) {
	env := newActivityEnv(t, adk.NewAgentRegistry())
	_, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
		AgentName: "ghost", UserID: "u", SessionID: "s", Message: userMessage("hi"),
	})
	require.Equal(t, adk.ErrTypeNonRetryable, appErrorType(t, err))
}

// TestStatefulMcpUnsupported: a factory that needs a long-lived cross-turn MCP
// session is rejected non-retryably with the documented error.
func TestStatefulMcpUnsupported(t *testing.T) {
	reg := adk.NewAgentRegistry()
	reg.Register("mcp", func(_ context.Context) (*adk.AgentRunner, error) {
		// A real runner/service plus the unsupported-stateful-MCP flag.
		f := newAgentFactory("mcp", adk.NewMockModel("mcp", modelMessage("x")))
		ar, err := f(context.Background())
		if err != nil {
			return nil, err
		}
		ar.RequiresStatefulMCP = true
		return ar, nil
	})
	env := newActivityEnv(t, reg)
	_, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
		AgentName: "mcp", AppName: "mcp", UserID: "u", SessionID: "s", Message: userMessage("hi"),
	})
	require.Equal(t, adk.ErrTypeNonRetryable, appErrorType(t, err))
}

// TestConfirmationRejected: a rejected tool confirmation surfacing from the
// factory is classified ADKConfirmationRejected and not retried.
func TestConfirmationRejected(t *testing.T) {
	reg := adk.NewAgentRegistry()
	reg.Register("hitl", func(_ context.Context) (*adk.AgentRunner, error) {
		return nil, fmt.Errorf("user declined: %w", tool.ErrConfirmationRejected)
	})
	env := newActivityEnv(t, reg)
	_, err := env.ExecuteActivity(adk.ActivityNameRunTurn, adk.TurnInput{
		AgentName: "hitl", AppName: "hitl", UserID: "u", SessionID: "s", Message: userMessage("hi"),
	})
	require.Equal(t, adk.ErrTypeConfirmationRejected, appErrorType(t, err))
}

// TestRetryExhaustedFailsWorkflow: a turn that keeps failing with a retryable
// error exhausts the RetryPolicy and fails the session workflow with the
// classified cause preserved.
func TestRetryExhaustedFailsWorkflow(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()

	var attempts int
	env.RegisterActivityWithOptions(
		func(_ context.Context, _ adk.TurnInput) (adk.TurnResult, error) {
			attempts++
			return adk.TurnResult{}, temporal.NewApplicationError("transient model failure", adk.ErrTypeAgentError)
		},
		activity.RegisterOptions{Name: adk.ActivityNameRunTurn},
	)

	opts := adk.Options{DefaultActivityOptions: adk.ActivityOptions{
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2},
	}}
	in := adk.NewAgentSessionInput(opts, "flaky", "flaky", "u", "s")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(adk.SignalSendMessage, adk.TurnRequest{Message: userMessage("hi")})
	}, 0)

	env.ExecuteWorkflow(adk.AgentSessionWorkflow, in)

	require.True(t, env.IsWorkflowCompleted())
	wErr := env.GetWorkflowError()
	require.Error(t, wErr)
	require.GreaterOrEqual(t, attempts, 2, "RetryPolicy should have retried the turn")

	// The classified ADKAgentError cause survives to the workflow failure.
	require.Equal(t, adk.ErrTypeAgentError, appErrorType(t, wErr))
}

// silence unused imports when the runner/session packages are only used
// indirectly via the factory helper.
var (
	_ = runner.Config{}
	_ = session.GetRequest{}
)
