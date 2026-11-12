package temporalnexus_test

import (
	"context"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestNexusDynamicWorkflowTestEnvironment checks that named Nexus operation handler takes precedence over the dynamic handler.
func TestNexusDynamicWorkflowTestEnvironment(t *testing.T) {
	for _, tc := range []struct{ operation, want string }{
		{"named", "static"},
		{"echo", "echo:input"},
		{"another", "another:input"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			service := nexus.NewService("tools")

			// Register a dynamic operation that simply returns the <operation_name>:<input>.
			require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
				temporalnexus.DynamicTemporalOperationOptions{
					Start: func(ctx context.Context, _ temporalnexus.NexusClient, input *nexus.LazyValue, opts temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
						require.NotEmpty(t, opts.RequestID)
						var value string
						if err := input.Consume(&value); err != nil {
							return temporalnexus.TemporalOperationResult[any]{}, err
						}
						return temporalnexus.NewSyncResult[any](nexus.ExtractHandlerInfo(ctx).Operation + ":" + value), nil
					},
				},
			)))
			service.MustRegister(nexus.NewSyncOperation("named", func(context.Context, string, nexus.StartOperationOptions) (string, error) {
				return "static", nil
			}))
			caller := func(ctx workflow.Context) (string, error) {
				var value string
				err := workflow.NewNexusClient("endpoint", "tools").ExecuteOperation(ctx, tc.operation, "input", workflow.NexusOperationOptions{}).Get(ctx, &value)
				return value, err
			}
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterNexusService(service)
			started := false
			env.SetOnNexusOperationStartedListener(func(service, operation string, input converter.EncodedValue) {
				require.Equal(t, "tools", service)
				require.Equal(t, tc.operation, operation)
				var value string
				require.NoError(t, input.Get(&value))
				require.Equal(t, "input", value)
				started = true
			})
			env.ExecuteWorkflow(caller)
			require.True(t, started)
			require.NoError(t, env.GetWorkflowError())
			var result string
			require.NoError(t, env.GetWorkflowResult(&result))
			require.Equal(t, tc.want, result)
			if tc.operation != "named" {
				require.NotNil(t, service.Operation(tc.operation))
			}
		})
	}
}

// TestNexusDynamicWorkflowTestEnvironmentMock checks that a Nexus operation handler
// that's mocked in the test environment takes precedence over a dynamic handler.
func TestNexusDynamicWorkflowTestEnvironmentMock(t *testing.T) {
	service := nexus.NewService("tools")

	// Registers a dynamic handler that always throws.
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
		temporalnexus.DynamicTemporalOperationOptions{
			Start: func(context.Context, temporalnexus.NexusClient, *nexus.LazyValue, temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
				return temporalnexus.TemporalOperationResult[any]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "dynamic handler must not run for a mocked name")
			},
		},
	)))
	caller := func(ctx workflow.Context) (string, error) {
		var value string
		err := workflow.NewNexusClient("endpoint", "tools").ExecuteOperation(ctx, "echo", "input", workflow.NexusOperationOptions{}).Get(ctx, &value)
		return value, err
	}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterNexusService(service)
	require.PanicsWithValue(t,
		`operation "echo" expects input type *nexus.LazyValue, got string`,
		func() { env.OnNexusOperation(service, "echo", "input", mock.Anything) },
	)

	// Add a mock handler for an 'echo' operation for 'input' input. We want to ensure this is not shadowed
	// by an actual dynamic handler registered on the service.
	env.OnNexusOperation(service, nexus.NewOperationReference[string, string]("echo"), "input", mock.Anything).
		Return(&nexus.HandlerStartOperationResultSync[string]{Value: "mocked"}, nil)
	env.ExecuteWorkflow(caller)
	require.NoError(t, env.GetWorkflowError())
	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "mocked", result)
}

// TestNexusDynamicWorkflowTestEnvironmentMixedMocks checks that a Nexus service with all 3 of:
// - registered named Nexus operation handler
// - mocked Nexus operation handler in the test environment
// - dynamic handler
// works as expected in test. Operations that have named handlers (mocked or registered) should
// execute via those handlers. Other operations get picked up by the dynamic handler.
func TestNexusDynamicWorkflowTestEnvironmentMixedMocks(t *testing.T) {
	service := nexus.NewService("tools")
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
		temporalnexus.DynamicTemporalOperationOptions{
			Start: func(ctx context.Context, _ temporalnexus.NexusClient, input *nexus.LazyValue, _ temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
				var value string
				if err := input.Consume(&value); err != nil {
					return temporalnexus.TemporalOperationResult[any]{}, err
				}
				return temporalnexus.NewSyncResult[any](nexus.ExtractHandlerInfo(ctx).Operation + ":" + value), nil
			},
		},
	)))
	service.MustRegister(nexus.NewSyncOperation("named", func(_ context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
		return "named:" + input, nil
	}))
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterNexusService(service)
	env.OnNexusOperation(service, nexus.NewOperationReference[string, string]("mocked"), "input", mock.Anything).
		Return(&nexus.HandlerStartOperationResultSync[string]{Value: "mocked:input"}, nil).Once()
	var started []string
	env.SetOnNexusOperationStartedListener(func(service, operation string, input converter.EncodedValue) {
		require.Equal(t, "tools", service)
		var value string
		require.NoError(t, input.Get(&value))
		require.Equal(t, "input", value)
		started = append(started, operation)
	})
	env.ExecuteWorkflow(func(ctx workflow.Context) ([]string, error) {
		var results []string
		for _, operation := range []string{"mocked", "named", "unmatched"} {
			var result string
			if err := workflow.NewNexusClient("endpoint", "tools").ExecuteOperation(ctx, operation, "input", workflow.NexusOperationOptions{}).Get(ctx, &result); err != nil {
				return nil, err
			}
			results = append(results, result)
		}
		return results, nil
	})
	require.NoError(t, env.GetWorkflowError())
	var results []string
	require.NoError(t, env.GetWorkflowResult(&results))
	require.Equal(t, []string{"mocked:input", "named:input", "unmatched:input"}, results)
	require.Equal(t, []string{"mocked", "named", "unmatched"}, started)
	env.AssertExpectations(t)
}

func waitForCancel(ctx workflow.Context, _ string) (string, error) {
	return "", workflow.Await(ctx, func() bool { return false })
}

// TestNexusDynamicWorkflowTestEnvironmentCancel checks that the test environment cancels the Workflow started by the fallback.
func TestNexusDynamicWorkflowTestEnvironmentCancel(t *testing.T) {
	service := nexus.NewService("tools")
	require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
		temporalnexus.DynamicTemporalOperationOptions{
			Start: func(ctx context.Context, nc temporalnexus.NexusClient, input *nexus.LazyValue, _ temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
				var id string
				if err := input.Consume(&id); err != nil {
					return temporalnexus.TemporalOperationResult[any]{}, err
				}
				return temporalnexus.StartUntypedWorkflow[any](ctx, nc, client.StartWorkflowOptions{ID: id}, waitForCancel, id)
			},
		},
	)))
	caller := func(ctx workflow.Context) error {
		ctx, cancel := workflow.WithCancel(ctx)
		fut := workflow.NewNexusClient("endpoint", "tools").ExecuteOperation(ctx, "wait", "backing-id", workflow.NexusOperationOptions{})
		var exec workflow.NexusOperationExecution
		if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
			return err
		}
		cancel()
		return fut.Get(ctx, nil)
	}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForCancel)
	env.RegisterNexusService(service)
	env.ExecuteWorkflow(caller)
	require.True(t, env.IsWorkflowCompleted())
	var canceledErr *temporal.CanceledError
	require.ErrorAs(t, env.GetWorkflowError(), &canceledErr)
}

// TestNexusDynamicWorkflowTestEnvironmentError checks that fallback errors reach the caller without becoming missing-operation panics.
func TestNexusDynamicWorkflowTestEnvironmentError(t *testing.T) {
	for _, errorType := range []nexus.HandlerErrorType{nexus.HandlerErrorTypeNotFound, nexus.HandlerErrorTypeBadRequest} {
		t.Run(string(errorType), func(t *testing.T) {
			service := nexus.NewService("tools")
			require.NoError(t, service.RegisterDynamic(temporalnexus.MustNewDynamicTemporalOperation(
				temporalnexus.DynamicTemporalOperationOptions{
					Start: func(context.Context, temporalnexus.NexusClient, *nexus.LazyValue, temporalnexus.StartTemporalOperationOptions) (temporalnexus.TemporalOperationResult[any], error) {
						return temporalnexus.TemporalOperationResult[any]{}, nexus.NewHandlerErrorf(errorType, "rejected by fallback")
					},
				},
			)))
			caller := func(ctx workflow.Context) error {
				return workflow.NewNexusClient("endpoint", "tools").ExecuteOperation(ctx, "reject", "input", workflow.NexusOperationOptions{}).Get(ctx, nil)
			}
			suite := testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterNexusService(service)
			env.ExecuteWorkflow(caller)
			require.True(t, env.IsWorkflowCompleted())
			var handlerErr *nexus.HandlerError
			require.ErrorAs(t, env.GetWorkflowError(), &handlerErr)
			require.Equal(t, errorType, handlerErr.Type)
			require.Equal(t, "rejected by fallback", handlerErr.Message)
		})
	}
}
