package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
)

type metadataContextConverter struct {
	converter.DataConverter
	wantContext context.Context
	context     context.Context
	serialized  bool
}

func (dc metadataContextConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.context = ctx
	return dc
}

func (dc metadataContextConverter) WithWorkflowContext(Context) converter.DataConverter {
	return dc
}

func (dc metadataContextConverter) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	dc.serialized = dc.context == dc.wantContext &&
		ctx == (converter.WorkflowSerializationContext{Namespace: "context-test", WorkflowID: "workflow"})
	return dc
}

func (dc metadataContextConverter) FromPayload(payload *commonpb.Payload, value any) error {
	if _, ok := value.(*string); ok {
		if dc.context != dc.wantContext {
			return fmt.Errorf("metadata decoding without exact caller context")
		}
		if !dc.serialized {
			return fmt.Errorf("metadata decoding without workflow serialization context after caller context")
		}
	}
	return dc.DataConverter.FromPayload(payload, value)
}

func newMetadataContextClient(t *testing.T, ctx context.Context) (*workflowservicemock.MockWorkflowServiceClient, *WorkflowClient) {
	t.Helper()
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	client := NewServiceClient(service, nil, ClientOptions{
		Namespace: "context-test",
		DataConverter: metadataContextConverter{
			DataConverter: converter.GetDefaultDataConverter(),
			wantContext:   ctx,
		},
	})
	return service, client
}

func TestConverterContext_WorkflowMetadata(t *testing.T) {
	for _, field := range []string{"summary", "details", "memo"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
			service, client := newMetadataContextClient(t, ctx)
			payload, err := converter.GetDefaultDataConverter().ToPayload("metadata")
			require.NoError(t, err)
			service.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.DescribeWorkflowExecutionResponse{
					WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
						Execution:        &commonpb.WorkflowExecution{WorkflowId: "workflow"},
						SearchAttributes: &commonpb.SearchAttributes{},
						Memo:             &commonpb.Memo{Fields: map[string]*commonpb.Payload{"memo": payload}},
					},
					ExecutionConfig: &workflowpb.WorkflowExecutionConfig{
						UserMetadata: &sdkpb.UserMetadata{Summary: payload, Details: payload},
					},
				}, nil)
			description, err := client.DescribeWorkflow(ctx, "workflow", "")
			require.NoError(t, err)
			var got string
			switch field {
			case "summary":
				got, err = description.GetStaticSummary()
			case "details":
				got, err = description.GetStaticDetails()
			case "memo":
				err = description.GetMemoValue("memo", &got)
			}
			require.NoError(t, err)
			require.Equal(t, "metadata", got)
		})
	}
}

func TestConverterContext_ScheduleMetadata(t *testing.T) {
	for _, operation := range []string{"Describe", "Update"} {
		for _, field := range []string{"summary", "details"} {
			t.Run(operation+"/"+field, func(t *testing.T) {
				ctx := context.WithValue(t.Context(), converterBindingKey{}, "bound")
				service, client := newMetadataContextClient(t, ctx)
				payload, err := converter.GetDefaultDataConverter().ToPayload("metadata")
				require.NoError(t, err)
				metadata := &sdkpb.UserMetadata{}
				if field == "summary" {
					metadata.Summary = payload
				} else {
					metadata.Details = payload
				}
				service.EXPECT().DescribeSchedule(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&workflowservice.DescribeScheduleResponse{
						Schedule: &schedulepb.Schedule{
							Action: &schedulepb.ScheduleAction{
								Action: &schedulepb.ScheduleAction_StartWorkflow{
									StartWorkflow: &workflowpb.NewWorkflowExecutionInfo{
										WorkflowId:   "workflow",
										WorkflowType: &commonpb.WorkflowType{Name: "workflow-type"},
										TaskQueue:    &taskqueuepb.TaskQueue{Name: "task-queue"},
										UserMetadata: metadata,
									},
								},
							},
						},
						Info: &schedulepb.ScheduleInfo{},
					}, nil)
				handle := client.ScheduleClient().GetHandle(t.Context(), "schedule")
				var description *ScheduleDescription
				if operation == "Describe" {
					description, err = handle.Describe(ctx)
				} else {
					err = handle.Update(ctx, ScheduleUpdateOptions{
						DoUpdate: func(input ScheduleUpdateInput) (*ScheduleUpdate, error) {
							description = &input.Description
							return nil, ErrSkipScheduleUpdate
						},
					})
				}
				require.NoError(t, err)
				require.NotNil(t, description)
				action := description.Schedule.Action.(*ScheduleWorkflowAction)
				if field == "summary" {
					require.Equal(t, "metadata", action.StaticSummary)
				} else {
					require.Equal(t, "metadata", action.StaticDetails)
				}
			})
		}
	}
}
