package workflow_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestWithCancelCause(t *testing.T) {
	for _, parentFirst := range []bool{false, true} {
		name := "child first"
		if parentFirst {
			name = "parent first"
		}
		t.Run(name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.ExecuteWorkflow(func(root workflow.Context) error {
				parent, cancelParent := workflow.WithCancelCause(root)
				child, cancelChild := workflow.WithCancelCause(parent)
				parentCause := errors.New("parent canceled")
				childCause := errors.New("child canceled")
				assert.Nil(t, workflow.Cause(child))
				assert.Nil(t, child.Err())

				expectedChildCause := childCause
				if parentFirst {
					cancelParent(parentCause)
					cancelChild(childCause)
					expectedChildCause = parentCause
				} else {
					cancelChild(childCause)
					cancelParent(parentCause)
				}
				cancelParent(errors.New("ignored parent cause"))
				cancelChild(errors.New("ignored child cause"))

				assert.Same(t, parentCause, workflow.Cause(parent))
				assert.Same(t, expectedChildCause, workflow.Cause(child))
				assert.Same(t, workflow.ErrCanceled, parent.Err())
				assert.Same(t, workflow.ErrCanceled, child.Err())
				assert.Nil(t, workflow.Cause(root))
				assert.Nil(t, root.Err())
				_, more := parent.Done().ReceiveAsyncWithMoreFlag(nil)
				assert.False(t, more)
				_, more = child.Done().ReceiveAsyncWithMoreFlag(nil)
				assert.False(t, more)
				return nil
			})
			require.NoError(t, env.GetWorkflowError())
		})
	}
}

func TestCancelCauseInheritance(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(root workflow.Context) error {
		parent, cancel := workflow.WithCancelCause(root)
		child, cancelChild := workflow.WithCancel(parent)
		defer cancelChild()
		disconnected, cancelDisconnected := workflow.NewDisconnectedContext(parent)
		defer cancelDisconnected()
		cause := errors.New("no longer needed")
		cancel(cause)

		type key struct{}
		assert.Same(t, cause, workflow.Cause(workflow.WithValue(child, key{}, true)))
		assert.Same(t, workflow.ErrCanceled, child.Err())

		lateChild, cancelLateChild := workflow.WithCancel(parent)
		defer cancelLateChild()
		assert.Same(t, cause, workflow.Cause(lateChild))
		assert.Same(t, workflow.ErrCanceled, lateChild.Err())

		assert.Nil(t, workflow.Cause(disconnected))
		assert.Nil(t, disconnected.Err())
		lateDisconnected, cancelLateDisconnected := workflow.NewDisconnectedContext(parent)
		defer cancelLateDisconnected()
		assert.Nil(t, workflow.Cause(lateDisconnected))
		assert.Nil(t, lateDisconnected.Err())
		cancelDisconnected()
		assert.Same(t, workflow.ErrCanceled, workflow.Cause(disconnected))
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
}

func TestCancelCauseDefaults(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(root workflow.Context) error {
		parent, cancelParent := workflow.WithCancelCause(root)
		child, cancelChild := workflow.WithCancelCause(parent)
		cancelParent(nil)
		cancelChild(errors.New("too late"))
		assert.Same(t, workflow.ErrCanceled, workflow.Cause(parent))
		assert.Same(t, workflow.ErrCanceled, workflow.Cause(child))

		legacy, cancelLegacy := workflow.WithCancel(root)
		cancelLegacy()
		assert.Same(t, workflow.ErrCanceled, workflow.Cause(legacy))
		assert.Same(t, workflow.ErrCanceled, legacy.Err())
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
}

func TestCancellationRequestReasonIsNotChildCompletionError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	child := func(ctx workflow.Context) error {
		ctx.Done().Receive(ctx, nil)
		return ctx.Err()
	}
	env.RegisterWorkflow(child)
	env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: "child"})
		future := workflow.ExecuteChildWorkflow(childCtx, child)
		var execution workflow.Execution
		if err := future.GetChildWorkflowExecution().Get(ctx, &execution); err != nil {
			return "", err
		}
		if err := workflow.RequestCancelExternalWorkflowWithOptions(ctx, execution.ID, execution.RunID,
			workflow.CancelExternalWorkflowOptions{Reason: "request reason only"}).Get(ctx, nil); err != nil {
			return "", err
		}
		err := future.Get(ctx, nil)
		var canceled *temporal.CanceledError
		if !errors.As(err, &canceled) {
			return "", fmt.Errorf("child error = %v, want CanceledError", err)
		}
		return canceled.Error(), nil
	})
	require.NoError(t, env.GetWorkflowError())
	var message string
	require.NoError(t, env.GetWorkflowResult(&message))
	require.Equal(t, "canceled", message)
}
