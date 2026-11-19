package workflow_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type localVarContextKey struct{}

func TestLocalVarRequiresInitialization(t *testing.T) {
	var local workflow.LocalVar[string]
	require.PanicsWithValue(t, "workflow.LocalVar is not initialized; use workflow.NewLocalVar(ctx)", func() {
		local.Get(nil)
	})
	require.PanicsWithValue(t, "workflow.LocalVar is not initialized; use workflow.NewLocalVar(ctx)", func() {
		local.Set(nil, "value")
	})
}

func TestLocalVar(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		localVarString := workflow.NewLocalVar[string](ctx)
		localVarOther := workflow.NewLocalVar[string](ctx)
		localVarPointer := workflow.NewLocalVar[*int](ctx)
		localVarAny := workflow.NewLocalVar[any](ctx)

		if value := localVarString.Get(ctx); value != "" {
			return fmt.Errorf("unset string LocalVar = %q, want empty", value)
		}
		if value := localVarPointer.Get(ctx); value != nil {
			return fmt.Errorf("unset pointer LocalVar = %v, want nil", value)
		}
		if value := localVarAny.Get(ctx); value != nil {
			return fmt.Errorf("unset any LocalVar = %v, want nil", value)
		}

		localVarString.Set(ctx, "root")
		localVarOther.Set(ctx, "other")
		copied := localVarString
		if value := copied.Get(ctx); value != "root" {
			return fmt.Errorf("copied LocalVar = %q, want root", value)
		}
		copied.Set(ctx, "copy")
		if value := localVarString.Get(ctx); value != "copy" {
			return fmt.Errorf("original LocalVar after setting copy = %q, want copy", value)
		}
		if value := localVarString.Get(workflow.WithValue(ctx, localVarContextKey{}, true)); value != "copy" {
			return fmt.Errorf("LocalVar through WithValue = %q, want copy", value)
		}

		cancelCtx, cancel := workflow.WithCancel(ctx)
		defer cancel()
		if value := localVarString.Get(cancelCtx); value != "copy" {
			return fmt.Errorf("LocalVar through WithCancel = %q, want copy", value)
		}

		disconnectedCtx, cancelDisconnected := workflow.NewDisconnectedContext(ctx)
		defer cancelDisconnected()
		if value := localVarString.Get(disconnectedCtx); value != "copy" {
			return fmt.Errorf("LocalVar through NewDisconnectedContext = %q, want copy", value)
		}

		done := workflow.NewChannel(ctx)
		workflow.Go(cancelCtx, func(ctx workflow.Context) {
			localVarString.Set(ctx, "coroutine")
			done.Send(ctx, nil)
		})
		done.Receive(ctx, nil)
		if value := localVarString.Get(ctx); value != "coroutine" {
			return fmt.Errorf("LocalVar after coroutine Set = %q, want coroutine", value)
		}
		if value := localVarOther.Get(ctx); value != "other" {
			return fmt.Errorf("second LocalVar = %q, want other", value)
		}

		pointerValue := 42
		localVarPointer.Set(ctx, &pointerValue)
		if value := localVarPointer.Get(ctx); value == nil || *value != pointerValue {
			return fmt.Errorf("pointer LocalVar = %v, want %d", value, pointerValue)
		}
		localVarAny.Set(ctx, nil)
		if value := localVarAny.Get(ctx); value != nil {
			return fmt.Errorf("nil any LocalVar = %v, want nil", value)
		}
		return nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestLocalVarRejectsContextFromAnotherWorkflowRun(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	var local workflow.LocalVar[string]
	first := suite.NewTestWorkflowEnvironment()
	first.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		local = workflow.NewLocalVar[string](ctx)
		local.Set(ctx, "first")
		return local.Get(ctx), nil
	})
	require.NoError(t, first.GetWorkflowError())
	var firstResult string
	require.NoError(t, first.GetWorkflowResult(&firstResult))
	require.Equal(t, "first", firstResult)

	second := suite.NewTestWorkflowEnvironment()
	second.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		return local.Get(ctx), nil
	})
	require.ErrorContains(t, second.GetWorkflowError(), "context belongs to a different workflow run")
}

func TestLocalVarQueryReadAndWrite(t *testing.T) {
	const (
		getQuery = "get-local-var"
		setQuery = "set-local-var"
	)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var setQueryErr error
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(getQuery)
		require.NoError(t, err)
		var result string
		require.NoError(t, value.Get(&result))
		require.Equal(t, "workflow", result)

		_, setQueryErr = env.QueryWorkflow(setQuery)
		env.SignalWorkflow("finish", nil)
	}, time.Hour)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		localVarString := workflow.NewLocalVar[string](ctx)
		localVarString.Set(ctx, "workflow")
		if err := workflow.SetQueryHandler(ctx, getQuery, func() (string, error) {
			return localVarString.Get(ctx), nil
		}); err != nil {
			return err
		}
		if err := workflow.SetQueryHandler(ctx, setQuery, func() (string, error) {
			localVarString.Set(ctx, "query")
			return localVarString.Get(ctx), nil
		}); err != nil {
			return err
		}
		workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
		return nil
	})

	require.NoError(t, env.GetWorkflowError())
	require.Error(t, setQueryErr)
	require.Contains(t, setQueryErr.Error(), "query handler must not use temporal context")
}

func TestLocalVarSetInSideEffectIsRejected(t *testing.T) {
	tests := map[string]func(workflow.Context){
		"SideEffect": func(ctx workflow.Context) {
			localVarString := workflow.NewLocalVar[string](ctx)
			workflow.SideEffect(ctx, func(ctx workflow.Context) any {
				localVarString.Set(ctx, "side effect")
				return nil
			})
		},
		"MutableSideEffect": func(ctx workflow.Context) {
			localVarString := workflow.NewLocalVar[string](ctx)
			workflow.MutableSideEffect(ctx, "local-var", func(ctx workflow.Context) any {
				localVarString.Set(ctx, "mutable side effect")
				return nil
			}, func(a, b any) bool { return a == b })
		},
	}

	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				run(ctx)
				return nil
			})
			require.Error(t, env.GetWorkflowError())
		})
	}
}

func TestLocalVarSetInUpdateValidatorIsRejected(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id", &testsuite.TestUpdateCallback{})
	}, time.Second)
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		localVarString := workflow.NewLocalVar[string](ctx)
		if err := workflow.SetUpdateHandlerWithOptions(ctx, "update", func(ctx workflow.Context) (string, error) {
			return localVarString.Get(ctx), nil
		}, workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context) error {
				localVarString.Set(ctx, "validator")
				return nil
			},
		}); err != nil {
			return err
		}
		return workflow.Sleep(ctx, time.Minute)
	})

	require.Error(t, env.GetWorkflowError())
}
