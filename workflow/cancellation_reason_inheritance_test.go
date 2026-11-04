package workflow_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestExternalCancellationReasonInheritance(t *testing.T) {
	for _, withOptions := range []bool{false, true} {
		api := "legacy"
		if withOptions {
			api = "with options"
		}
		t.Run(api, func(t *testing.T) {
			for _, test := range []struct {
				name           string
				cause          error
				ordinaryCancel bool
				uncanceled     bool
				explicitReason string
				wantReason     string
			}{
				{name: "local cause", cause: errors.New("no longer needed"), wantReason: "no longer needed"},
				{name: "ordinary cancel", ordinaryCancel: true},
				{name: "nil cause"},
				{name: "exact sentinel", cause: workflow.ErrCanceled},
				{name: "canceled message is not sentinel", cause: errors.New("canceled"), wantReason: "canceled"},
				{name: "uncanceled context", uncanceled: true},
				{
					name:           "explicit override",
					cause:          errors.New("inherited reason"),
					explicitReason: "explicit reason",
					wantReason:     "explicit reason",
				},
			} {
				if !withOptions && test.explicitReason != "" {
					continue
				}
				t.Run(test.name, func(t *testing.T) {
					var suite testsuite.WorkflowTestSuite
					env := suite.NewTestWorkflowEnvironment()
					env.OnRequestCancelExternalWorkflowWithOptions("other-namespace", "other-workflow", "other-run",
						workflow.CancelExternalWorkflowOptions{Reason: test.wantReason}).
						Return(func(namespace, workflowID, runID string, options workflow.CancelExternalWorkflowOptions) error {
							assert.Equal(t, test.wantReason, options.Reason)
							return nil
						}).Once()
					env.ExecuteWorkflow(func(root workflow.Context) error {
						ctx := workflow.WithWorkflowNamespace(root, "other-namespace")
						if test.ordinaryCancel {
							var cancel workflow.CancelFunc
							ctx, cancel = workflow.WithCancel(ctx)
							cancel()
						} else if !test.uncanceled {
							var cancel workflow.CancelCauseFunc
							ctx, cancel = workflow.WithCancelCause(ctx)
							cancel(test.cause)
						}
						var future workflow.Future
						if withOptions {
							future = workflow.RequestCancelExternalWorkflowWithOptions(ctx, "other-workflow", "other-run",
								workflow.CancelExternalWorkflowOptions{Reason: test.explicitReason})
						} else {
							future = workflow.RequestCancelExternalWorkflow(ctx, "other-workflow", "other-run")
						}
						// Waiting on the canceled request context would hide the request's result.
						return future.Get(root, nil)
					})
					require.NoError(t, env.GetWorkflowError())
					env.AssertExpectations(t)
				})
			}
		})
	}
}

func TestExternalCancellationReasonDisconnectedContext(t *testing.T) {
	for _, withOptions := range []bool{false, true} {
		api := "legacy"
		if withOptions {
			api = "with options"
		}
		t.Run(api, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.OnRequestCancelExternalWorkflowWithOptions("other-namespace", "other-workflow", "other-run",
				workflow.CancelExternalWorkflowOptions{}).Return(nil).Once()
			env.RegisterDelayedCallback(func() {
				env.CancelWorkflowWithReason("root cancellation reason")
			}, 0)
			env.ExecuteWorkflow(func(root workflow.Context) error {
				_ = workflow.Await(root, func() bool { return false })
				assert.EqualError(t, workflow.Cause(root), "root cancellation reason")
				ctx, cancel := workflow.NewDisconnectedContext(root)
				defer cancel()
				assert.Nil(t, workflow.Cause(ctx))
				ctx = workflow.WithWorkflowNamespace(ctx, "other-namespace")
				var future workflow.Future
				if withOptions {
					future = workflow.RequestCancelExternalWorkflowWithOptions(ctx, "other-workflow", "other-run",
						workflow.CancelExternalWorkflowOptions{})
				} else {
					future = workflow.RequestCancelExternalWorkflow(ctx, "other-workflow", "other-run")
				}
				return future.Get(ctx, nil)
			})
			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func TestChildCancellationReasonInheritance(t *testing.T) {
	for _, test := range []struct {
		name           string
		cause          error
		ordinaryCancel bool
		wantReason     string
		defaultCause   bool
	}{
		{name: "local cause", cause: errors.New("child no longer needed"), wantReason: "child no longer needed"},
		{name: "canceled message is not sentinel", cause: errors.New("canceled"), wantReason: "canceled"},
		{name: "nil cause", wantReason: "canceled", defaultCause: true},
		{name: "exact sentinel", cause: workflow.ErrCanceled, wantReason: "canceled", defaultCause: true},
		{name: "ordinary cancel", ordinaryCancel: true, wantReason: "canceled", defaultCause: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			child := func(ctx workflow.Context) (string, error) {
				ctx.Done().Receive(ctx, nil)
				assert.Same(t, workflow.ErrCanceled, ctx.Err())
				cause := workflow.Cause(ctx)
				if test.defaultCause {
					assert.Same(t, workflow.ErrCanceled, cause)
				} else {
					assert.NotSame(t, workflow.ErrCanceled, cause)
				}
				return cause.Error(), nil
			}
			env.RegisterWorkflow(child)
			env.ExecuteWorkflow(func(root workflow.Context) (string, error) {
				var ctx workflow.Context
				var cancel func()
				if test.ordinaryCancel {
					ctx, cancel = workflow.WithCancel(root)
				} else {
					var cancelCause workflow.CancelCauseFunc
					ctx, cancelCause = workflow.WithCancelCause(root)
					cancel = func() { cancelCause(test.cause) }
				}
				ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
					WorkflowID:          "child",
					WaitForCancellation: true,
				})
				future := workflow.ExecuteChildWorkflow(ctx, child)
				if err := future.GetChildWorkflowExecution().Get(root, nil); err != nil {
					return "", err
				}
				cancel()
				var reason string
				err := future.Get(root, &reason)
				return reason, err
			})
			require.NoError(t, env.GetWorkflowError())
			var reason string
			require.NoError(t, env.GetWorkflowResult(&reason))
			require.Equal(t, test.wantReason, reason)
		})
	}
}
