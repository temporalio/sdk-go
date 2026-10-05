package internal

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.temporal.io/sdk/converter"
)

func TestConverterContext_ActivityHeartbeatDetails(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.SetDataConverter(contextCheckedConverter{DataConverter: converter.GetDefaultDataConverter()})
	env.SetHeartbeatDetails("progress")
	activity := func(ctx context.Context) error {
		ctx = context.WithValue(ctx, converterBindingKey{}, "bound")
		var details contextCheckedValue
		if err := GetHeartbeatDetails(ctx, &details); err != nil {
			return err
		}
		require.Equal(t, contextCheckedValue("progress"), details)
		return nil
	}
	env.RegisterActivity(activity)
	_, err := env.ExecuteActivity(activity)
	require.NoError(t, err)
}

type activityBindingPropagator struct {
	ContextPropagator
}

func (activityBindingPropagator) Extract(ctx context.Context, reader HeaderReader) (context.Context, error) {
	payload, ok := reader.Get("converter-binding")
	if !ok {
		return ctx, nil
	}
	var value string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &value); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, converterBindingKey{}, value), nil
}

func (s *TaskHandlersTestSuite) TestConverterContext_ActivityCanceledDetails() {
	t := s.T()
	params := s.getTestWorkerExecutionParams()
	params.DataConverter = contextCheckedConverter{DataConverter: converter.GetDefaultDataConverter()}
	params.ContextPropagators = []ContextPropagator{activityBindingPropagator{}}
	params.activityCancellationCallbacks = newActivityCancellationCallbacks()
	token := []byte("context-binding")
	s.registry.RegisterActivityWithOptions(func(ctx context.Context) error {
		require.Equal(t, "bound", ctx.Value(converterBindingKey{}))
		require.True(t, params.activityCancellationCallbacks.cancel(token))
		<-ctx.Done()
		return NewCanceledError(contextCheckedValue("details"))
	}, RegisterActivityOptions{Name: "contextBindingCancellation"})
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	handler := newActivityTaskHandler(&WorkflowClient{workflowService: service}, params, s.registry)
	header, err := converter.GetDefaultDataConverter().ToPayload("bound")
	require.NoError(t, err)
	now := timestamppb.New(time.Now())
	task := &workflowservice.PollActivityTaskQueueResponse{
		TaskToken:              token,
		WorkflowExecution:      &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"},
		WorkflowType:           &commonpb.WorkflowType{Name: "workflowType"},
		ActivityType:           &commonpb.ActivityType{Name: "contextBindingCancellation"},
		ActivityId:             "activity",
		WorkflowNamespace:      params.Namespace,
		ScheduledTime:          now,
		StartedTime:            now,
		StartToCloseTimeout:    durationpb.New(time.Minute),
		ScheduleToCloseTimeout: durationpb.New(time.Minute),
		Header:                 &commonpb.Header{Fields: map[string]*commonpb.Payload{"converter-binding": header}},
	}
	result, err := handler.Execute(params.TaskQueue, task)
	require.NoError(t, err)
	response, ok := result.response.(*workflowservice.RespondActivityTaskCanceledRequest)
	require.True(t, ok, "expected cancellation response, got %T", result.response)
	var details contextCheckedValue
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(response.Details, &details))
	require.Equal(t, contextCheckedValue("details"), details)
}
