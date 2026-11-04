package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func TestNexusTestClientCancellationCause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		legacy bool
	}{
		{name: "reason", reason: "  no longer needed  "},
		{name: "empty reason"},
		{name: "legacy", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			client := &testSuiteClientForNexusOperations{env: env.impl}
			ctx := context.WithValue(t.Context(), IsWorkflowRunOpContextKey, true)
			result := make(chan error, 1)
			env.SetOnChildWorkflowStartedListener(func(*WorkflowInfo, Context, converter.EncodedValues) {
				// The client waits for a callback on the test environment's event loop.
				go func() {
					if tc.legacy {
						result <- client.CancelWorkflow(ctx, "nexus-cancel-target", "")
					} else {
						result <- client.CancelWorkflowWithOptions(ctx, CancelWorkflowOptions{
							WorkflowID: "nexus-cancel-target",
							Reason:     tc.reason,
						})
					}
				}()
			})
			child := func(ctx Context) (string, error) {
				_ = Await(ctx, func() bool { return false })
				return Cause(ctx).Error(), nil
			}
			env.RegisterWorkflow(child)
			env.ExecuteWorkflow(func(ctx Context) (string, error) {
				ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
					WorkflowID:          "nexus-cancel-target",
					WaitForCancellation: true,
				})
				var cause string
				err := ExecuteChildWorkflow(ctx, child).Get(ctx, &cause)
				return cause, err
			})
			require.NoError(t, <-result)
			require.NoError(t, env.GetWorkflowError())
			var cause string
			require.NoError(t, env.GetWorkflowResult(&cause))
			if tc.reason == "" {
				require.Equal(t, ErrCanceled.Error(), cause)
			} else {
				require.Equal(t, tc.reason, cause)
			}
		})
	}
}

func TestNexusTestClientCancellationRequiresWorkflowRunContext(t *testing.T) {
	client := &testSuiteClientForNexusOperations{}
	for _, ctx := range []context.Context{
		t.Context(),
		context.WithValue(t.Context(), IsWorkflowRunOpContextKey, false),
	} {
		require.PanicsWithValue(t, "not implemented in the test environment", func() {
			_ = client.CancelWorkflow(ctx, "target", "")
		})
		require.PanicsWithValue(t, "not implemented in the test environment", func() {
			_ = client.CancelWorkflowWithOptions(ctx, CancelWorkflowOptions{WorkflowID: "target", Reason: "reason"})
		})
	}
}

type captureNexusEnv struct {
	WorkflowEnvironment
	started   func(token string, err error)
	completed func(*commonpb.Payload, error)
}

func (e *captureNexusEnv) ExecuteNexusOperation(
	_ ExecuteNexusOperationParams,
	callback func(*commonpb.Payload, error),
	startedHandler func(token string, e error),
) int64 {
	e.started = startedHandler
	e.completed = callback
	return 1
}

func (e *captureNexusEnv) AbandonNexusOperation(int64) {
	e.started = nil
	e.completed = nil
}

// deliverStarted and deliverCompleted mimic the event handlers, which invoke a callback only
// while it is still set (see handleNexusOperationStarted / handleNexusOperationCompleted).
func (e *captureNexusEnv) deliverStarted(token string, err error) {
	if e.started != nil {
		e.started(token, err)
	}
}

func (e *captureNexusEnv) deliverCompleted(result *commonpb.Payload, err error) {
	if e.completed != nil {
		e.completed(result, err)
	}
}

// This cannot be reproduced through the workflow test environment as an ordinary workflow: its
// Nexus operation handle independently guards duplicate starts/completions (see
// testNexusOperationHandle), which masks the unguarded callbacks in the real event handler. So we
// drive the interceptor directly and feed it the late events a real environment would deliver.
func TestNexusAbandonCancellationClearsCallbacks(t *testing.T) {
	interceptor, ctx := createRootTestContext()
	capture := &captureNexusEnv{WorkflowEnvironment: interceptor.env}
	interceptor.env = capture

	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		cancelCtx, cancel := WithCancel(ctx)
		interceptor.ExecuteNexusOperation(cancelCtx, ExecuteNexusOperationInput{
			Client:    NewNexusClient("endpoint", "service"),
			Operation: "operation",
			Options:   NexusOperationOptions{CancellationType: NexusOperationCancellationTypeAbandon},
		})
		cancel()
		capture.deliverStarted("token", nil)
		capture.deliverCompleted(nil, nil)
	}, func() bool { return false })
	d.interceptor = interceptor
	defer d.Close()

	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
}
