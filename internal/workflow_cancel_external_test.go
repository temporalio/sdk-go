package internal

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/sdk/v1"
)

func TestRequestCancelExternalWorkflowReasonCommand(t *testing.T) {
	for _, reason := range []string{"", "no longer needed"} {
		t.Run(reason, func(t *testing.T) {
			env := &workflowEnvironmentImpl{commandsHelper: newCommandsHelper()}
			env.commandsHelper.setCurrentWorkflowTaskStartedEventID(3)
			groups := []*sdk.EventGroupMarker{{
				Variant: &sdk.EventGroupMarker_Label_{
					Label: &sdk.EventGroupMarker_Label{Id: "cancel-group"},
				},
			}}
			env.RequestCancelExternalWorkflow("namespace", "workflow-id", "run-id", reason, groups, nil)
			commands := env.commandsHelper.getCommands(true)
			require.Len(t, commands, 1)
			require.Equal(t, groups, commands[0].GetEventGroupMarkers())
			attributes := commands[0].GetRequestCancelExternalWorkflowExecutionCommandAttributes()
			require.Equal(t, "workflow-id", attributes.GetWorkflowId())
			require.Equal(t, "run-id", attributes.GetRunId())
			require.Equal(t, reason, attributes.GetReason())
		})
	}
}

func TestRequestCancelExternalWorkflowWithOptions(t *testing.T) {
	options := CancelExternalWorkflowOptions{
		Reason: "no longer needed",
	}
	for _, legacyMock := range []bool{false, true} {
		t.Run(map[bool]string{false: "reason-aware mock", true: "legacy mock"}[legacyMock], func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			if legacyMock {
				env.OnRequestCancelExternalWorkflow("other-namespace", "other-workflow", "other-run").
					Return(func(namespace, workflowID, runID string) error {
						require.Equal(t, "other-workflow", workflowID)
						return nil
					}).Once()
			} else {
				env.OnRequestCancelExternalWorkflowWithOptions("other-namespace", "other-workflow", "other-run", options).
					Return(func(namespace, workflowID, runID string, actualOptions CancelExternalWorkflowOptions) error {
						require.Equal(t, options, actualOptions)
						return nil
					}).Once()
			}
			env.ExecuteWorkflow(func(ctx Context) error {
				ctx = WithWorkflowNamespace(ctx, "other-namespace")
				return RequestCancelExternalWorkflowWithOptions(ctx, "other-workflow", "other-run", options).Get(ctx, nil)
			})
			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

type cancelExternalWorkflowInterceptor struct {
	WorkerInterceptorBase
	calls []string
}

func (w *cancelExternalWorkflowInterceptor) InterceptWorkflow(_ Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor {
	return &cancelExternalWorkflowInbound{WorkflowInboundInterceptorBase{Next: next}, w}
}

type cancelExternalWorkflowInbound struct {
	WorkflowInboundInterceptorBase
	interceptor *cancelExternalWorkflowInterceptor
}

func (w *cancelExternalWorkflowInbound) Init(outbound WorkflowOutboundInterceptor) error {
	return w.Next.Init(&cancelExternalWorkflowOutbound{WorkflowOutboundInterceptorBase{Next: outbound}, w.interceptor})
}

type cancelExternalWorkflowOutbound struct {
	WorkflowOutboundInterceptorBase
	interceptor *cancelExternalWorkflowInterceptor
}

func (w *cancelExternalWorkflowOutbound) RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	w.interceptor.calls = append(w.interceptor.calls, "legacy")
	return w.WorkflowOutboundInterceptorBase.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

func (w *cancelExternalWorkflowOutbound) RequestCancelExternalWorkflowWithOptions(ctx Context, workflowID, runID string, options CancelExternalWorkflowOptions) Future {
	w.interceptor.calls = append(w.interceptor.calls, "options")
	return w.WorkflowOutboundInterceptorBase.RequestCancelExternalWorkflowWithOptions(ctx, workflowID, runID, options)
}

func TestRequestCancelExternalWorkflowInterceptorRouting(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	interceptor := &cancelExternalWorkflowInterceptor{}
	env.SetWorkerOptions(WorkerOptions{Interceptors: []WorkerInterceptor{interceptor}})
	env.OnRequestCancelExternalWorkflowWithOptions(mock.Anything, "other-workflow", "other-run", CancelExternalWorkflowOptions{}).
		Return(nil).Once()
	options := CancelExternalWorkflowOptions{Reason: "no longer needed"}
	env.OnRequestCancelExternalWorkflowWithOptions(mock.Anything, "other-workflow", "other-run", options).
		Return(nil).Once()
	env.ExecuteWorkflow(func(ctx Context) error {
		if err := RequestCancelExternalWorkflow(ctx, "other-workflow", "other-run").Get(ctx, nil); err != nil {
			return err
		}
		return RequestCancelExternalWorkflowWithOptions(ctx, "other-workflow", "other-run", options).Get(ctx, nil)
	})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, []string{"legacy", "options"}, interceptor.calls)
	env.AssertExpectations(t)
}

func TestRequestCancelExternalWorkflowLegacyEmptyReason(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnRequestCancelExternalWorkflowWithOptions(mock.Anything, "other-workflow", "", CancelExternalWorkflowOptions{}).
		Return(nil).Once()
	env.ExecuteWorkflow(func(ctx Context) error {
		return RequestCancelExternalWorkflow(ctx, "other-workflow", "").Get(ctx, nil)
	})
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestRequestCancelExternalWorkflowWithOptionsRequiresWorkflowID(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx Context) error {
		err := RequestCancelExternalWorkflowWithOptions(ctx, "", "", CancelExternalWorkflowOptions{Reason: "reason"}).Get(ctx, nil)
		require.ErrorIs(t, err, errWorkflowIDNotSet)
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
}

func TestRequestCancelExternalWorkflowWithOptionsChildReason(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	child := func(ctx Context) (string, error) {
		_ = Await(ctx, func() bool { return false })
		require.ErrorIs(t, ctx.Err(), ErrCanceled)
		return Cause(ctx).Error(), nil
	}
	env.RegisterWorkflow(child)
	env.ExecuteWorkflow(func(ctx Context) error {
		ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
			WorkflowID:          "child-workflow",
			WaitForCancellation: true,
		})
		future := ExecuteChildWorkflow(ctx, child)
		var execution WorkflowExecution
		if err := future.GetChildWorkflowExecution().Get(ctx, &execution); err != nil {
			return err
		}
		if err := RequestCancelExternalWorkflowWithOptions(ctx, execution.ID, execution.RunID, CancelExternalWorkflowOptions{
			Reason: "child no longer needed",
		}).Get(ctx, nil); err != nil {
			return err
		}
		var reason string
		if err := future.Get(ctx, &reason); err != nil {
			return err
		}
		require.Equal(t, "child no longer needed", reason)
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
}

func TestTestEnvironmentCancelWorkflowWithReason(t *testing.T) {
	for _, reason := range []string{"", "no longer needed"} {
		t.Run(reason, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterDelayedCallback(func() {
				env.CancelWorkflowWithReason(reason)
			}, 0)
			env.ExecuteWorkflow(func(ctx Context) error {
				_ = Await(ctx, func() bool { return false })
				require.ErrorIs(t, ctx.Err(), ErrCanceled)
				if reason == "" {
					require.Same(t, ErrCanceled, Cause(ctx))
				} else {
					var canceled *CanceledError
					require.ErrorAs(t, Cause(ctx), &canceled)
					require.Equal(t, reason, canceled.Error())
				}
				return nil
			})
			require.NoError(t, env.GetWorkflowError())
		})
	}
}

func TestRequestCancelExternalWorkflowReplayIgnoresReason(t *testing.T) {
	for _, commandReason := range []string{"", "no longer needed"} {
		for _, eventReason := range []string{"", "no longer needed", "different reason"} {
			command := &commandpb.Command{
				CommandType: enumspb.COMMAND_TYPE_REQUEST_CANCEL_EXTERNAL_WORKFLOW_EXECUTION,
				Attributes: &commandpb.Command_RequestCancelExternalWorkflowExecutionCommandAttributes{
					RequestCancelExternalWorkflowExecutionCommandAttributes: &commandpb.RequestCancelExternalWorkflowExecutionCommandAttributes{
						WorkflowId: "workflow-id",
						Reason:     commandReason,
					},
				},
			}
			event := createTestEventRequestCancelExternalWorkflowExecutionInitiated(1,
				&historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes{
					WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflow-id"},
					Reason:            eventReason,
				})
			require.True(t, isCommandMatchEvent(command, event, nil))
		}
	}
}
