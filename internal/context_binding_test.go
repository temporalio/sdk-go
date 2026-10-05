package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	updatepb "go.temporal.io/api/update/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
)

type contextCheckedValue string
type converterBindingKey struct{}

type contextCheckedConverter struct {
	converter.DataConverter
	bound bool
}

func (dc contextCheckedConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.bound = ctx.Value(converterBindingKey{}) == "bound"
	return dc
}

func (dc contextCheckedConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	dc.bound = ctx.Value(converterBindingKey{}) == "bound"
	return dc
}

func (dc contextCheckedConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	for _, value := range values {
		if _, ok := value.(contextCheckedValue); ok && !dc.bound {
			return nil, fmt.Errorf("encoding without converter context")
		}
	}
	return dc.DataConverter.ToPayloads(values...)
}

func (dc contextCheckedConverter) FromPayloads(payloads *commonpb.Payloads, values ...any) error {
	for _, value := range values {
		if _, ok := value.(*contextCheckedValue); ok && !dc.bound {
			return fmt.Errorf("decoding without converter context")
		}
	}
	return dc.DataConverter.FromPayloads(payloads, values...)
}

func newContextBindingTestClient(t *testing.T) (*workflowservicemock.MockWorkflowServiceClient, *WorkflowClient) {
	t.Helper()
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	client := NewServiceClient(service, nil, ClientOptions{
		Namespace:     "context-test",
		DataConverter: contextCheckedConverter{DataConverter: converter.GetDefaultDataConverter()},
	})
	return service, client
}

func TestConverterContext_WorkflowResult(t *testing.T) {
	service, client := newContextBindingTestClient(t)
	payloads, err := converter.GetDefaultDataConverter().ToPayloads("value")
	require.NoError(t, err)
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&workflowservice.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: []*historypb.HistoryEvent{{
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
				WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: payloads},
			},
		}}}}, nil)
	// The retrieval context, not the context used to create the handle, binds decoding.
	ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
	var got contextCheckedValue
	require.NoError(t, client.GetWorkflow(t.Context(), "workflow", "run").Get(ctx, &got))
	require.Equal(t, contextCheckedValue("value"), got)
}

func TestConverterContext_Query(t *testing.T) {
	for _, args := range []bool{false, true} {
		t.Run(fmt.Sprintf("args=%v", args), func(t *testing.T) {
			service, client := newContextBindingTestClient(t)
			payloads, err := converter.GetDefaultDataConverter().ToPayloads("value")
			require.NoError(t, err)
			service.EXPECT().QueryWorkflow(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.QueryWorkflowResponse{QueryResult: payloads}, nil)
			ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
			var input []any
			if args {
				input = []any{contextCheckedValue("value")}
			}
			value, err := client.QueryWorkflow(ctx, "workflow", "run", "query", input...)
			require.NoError(t, err)
			var got contextCheckedValue
			require.NoError(t, value.Get(&got))
			require.Equal(t, contextCheckedValue("value"), got)
		})
	}
}

func TestConverterContext_Terminate(t *testing.T) {
	service, client := newContextBindingTestClient(t)
	service.EXPECT().TerminateWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.TerminateWorkflowExecutionResponse{}, nil)
	ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
	require.NoError(t, client.TerminateWorkflow(ctx, "workflow", "run", "reason", contextCheckedValue("value")))
}

func TestConverterContext_UpdateResult(t *testing.T) {
	for _, poll := range []bool{false, true} {
		t.Run(fmt.Sprintf("poll=%v", poll), func(t *testing.T) {
			service, client := newContextBindingTestClient(t)
			payloads, err := converter.GetDefaultDataConverter().ToPayloads("value")
			require.NoError(t, err)
			ref := &updatepb.UpdateRef{WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"}, UpdateId: "update"}
			outcome := &updatepb.Outcome{Value: &updatepb.Outcome_Success{Success: payloads}}
			response := &workflowservice.UpdateWorkflowExecutionResponse{
				UpdateRef: ref, Stage: enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED, Outcome: outcome,
			}
			if poll {
				response.Stage = enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ACCEPTED
				response.Outcome = nil
				service.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&workflowservice.PollWorkflowExecutionUpdateResponse{Outcome: outcome}, nil)
			}
			service.EXPECT().UpdateWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil)
			ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
			handle, err := client.UpdateWorkflow(ctx, UpdateWorkflowOptions{
				WorkflowID: "workflow", RunID: "run", UpdateID: "update", UpdateName: "update", WaitForStage: WorkflowUpdateStageCompleted,
			})
			require.NoError(t, err)
			var got contextCheckedValue
			require.NoError(t, handle.Get(ctx, &got))
			require.Equal(t, contextCheckedValue("value"), got)
		})
	}
}

func TestConverterContext_UpdateHandler(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetDataConverter(contextCheckedConverter{DataConverter: converter.GetDefaultDataConverter()})
	env.ExecuteWorkflow(func(ctx Context) error {
		ctx = WithValue(ctx, converterBindingKey{}, "bound")
		if err := SetUpdateHandler(ctx, "update", func(Context, contextCheckedValue) (contextCheckedValue, error) {
			return "value", nil
		}, UpdateHandlerOptions{}); err != nil {
			return err
		}
		dc := getWorkflowEnvOptions(ctx).updateHandlers["update"].dataConverter
		payloads, err := dc.ToPayloads(contextCheckedValue("value"))
		if err != nil {
			return err
		}
		var got contextCheckedValue
		return dc.FromPayloads(payloads, &got)
	})
	require.NoError(t, env.GetWorkflowError())
}

func TestConverterContext_MutableSideEffect(t *testing.T) {
	for _, mockResult := range []string{"none", "value", "function"} {
		t.Run(mockResult, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetDataConverter(contextCheckedConverter{DataConverter: converter.GetDefaultDataConverter()})
			if mockResult == "value" {
				env.OnMutableSideEffect("value").Return(contextCheckedValue("value")).Twice()
			}
			if mockResult == "function" {
				env.OnMutableSideEffect("value").Return(func(string) any { return contextCheckedValue("value") }).Twice()
			}
			env.ExecuteWorkflow(func(ctx Context) error {
				ctx = WithValue(ctx, converterBindingKey{}, "bound")
				for i := 0; i < 2; i++ {
					value := MutableSideEffect(ctx, "value", func(Context) any { return contextCheckedValue("value") },
						func(a, b any) bool { return a == b })
					var got contextCheckedValue
					if err := value.Get(&got); err != nil {
						return err
					}
					if got != "value" {
						return fmt.Errorf("got %q, want value", got)
					}
				}
				return nil
			})
			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}
