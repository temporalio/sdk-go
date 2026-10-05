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

func (dc contextCheckedConverter) ToPayload(value any) (*commonpb.Payload, error) {
	if _, ok := value.(contextCheckedValue); ok && !dc.bound {
		return nil, fmt.Errorf("encoding without converter context")
	}
	return dc.DataConverter.ToPayload(value)
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

func TestConverterContext_CancellationDetails(t *testing.T) {
	service, client := newContextBindingTestClient(t)
	payloads, err := converter.GetDefaultDataConverter().ToPayloads("details")
	require.NoError(t, err)
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&workflowservice.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: []*historypb.HistoryEvent{{
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCanceledEventAttributes{
				WorkflowExecutionCanceledEventAttributes: &historypb.WorkflowExecutionCanceledEventAttributes{Details: payloads},
			},
		}}}}, nil)
	ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
	err = client.GetWorkflow(t.Context(), "workflow", "run").Get(ctx, nil)
	var canceled *CanceledError
	require.ErrorAs(t, err, &canceled)
	var details contextCheckedValue
	require.NoError(t, canceled.Details(&details))
}

func TestConverterContext_DeploymentMetadata(t *testing.T) {
	service, client := newContextBindingTestClient(t)
	service.EXPECT().SetCurrentDeployment(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.SetCurrentDeploymentResponse{}, nil)
	ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
	require.NotPanics(t, func() {
		_, err := client.DeploymentClient().SetCurrent(ctx, DeploymentSetCurrentOptions{
			Deployment:     Deployment{SeriesName: "deployment", BuildID: "build"},
			MetadataUpdate: DeploymentMetadataUpdate{UpsertEntries: map[string]any{"value": contextCheckedValue("metadata")}},
		})
		require.NoError(t, err)
	})
}

func TestConverterContext_WorkerDeploymentMetadata(t *testing.T) {
	service, client := newContextBindingTestClient(t)
	service.EXPECT().UpdateWorkerDeploymentVersionMetadata(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.UpdateWorkerDeploymentVersionMetadataResponse{}, nil)
	ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
	require.NotPanics(t, func() {
		_, err := client.WorkerDeploymentClient().GetHandle("deployment").UpdateVersionMetadata(ctx, WorkerDeploymentUpdateVersionMetadataOptions{
			Version:        WorkerDeploymentVersion{DeploymentName: "deployment", BuildID: "build"},
			MetadataUpdate: WorkerDeploymentMetadataUpdate{UpsertEntries: map[string]any{"value": contextCheckedValue("metadata")}},
		})
		require.NoError(t, err)
	})
}

func TestConverterContext_SideEffectMock(t *testing.T) {
	for _, mockResult := range []string{"value", "function"} {
		t.Run(mockResult, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetDataConverter(contextCheckedConverter{DataConverter: converter.GetDefaultDataConverter()})
			if mockResult == "value" {
				env.OnSideEffect().Return(contextCheckedValue("value")).Once()
			} else {
				env.OnSideEffect().Return(func() any { return contextCheckedValue("value") }).Once()
			}
			env.ExecuteWorkflow(func(ctx Context) error {
				ctx = WithValue(ctx, converterBindingKey{}, "bound")
				var result contextCheckedValue
				return SideEffect(ctx, func(Context) any { return contextCheckedValue("value") }).Get(&result)
			})
			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}
