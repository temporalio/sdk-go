package internal

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	activitypb "go.temporal.io/api/activity/v1"
	commonpb "go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Timestamps the server leaves unset (e.g. CloseTime of a running execution) must
// surface as the zero time.Time, not the Unix epoch that a nil proto AsTime() returns.

func TestDescribeActivityUnsetTimestampsAreZero(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	scheduled := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	service.EXPECT().DescribeActivityExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeActivityExecutionResponse{
			Info: &activitypb.ActivityExecutionInfo{
				ActivityId:       "activity-id",
				RunId:            "run-id",
				ScheduleTime:     timestamppb.New(scheduled),
				SearchAttributes: &commonpb.SearchAttributes{},
			},
		}, nil)

	handle := client.GetActivityHandle(ClientGetActivityHandleOptions{ActivityID: "activity-id", RunID: "run-id"})
	desc, err := handle.Describe(context.Background(), ClientDescribeActivityOptions{})
	require.NoError(t, err)
	require.Equal(t, scheduled, desc.ScheduleTime)
	require.True(t, desc.CloseTime.IsZero())
	require.True(t, desc.ExecutionTime.IsZero())
	require.True(t, desc.LastHeartbeatTime.IsZero())
	require.True(t, desc.LastStartedTime.IsZero())
	require.True(t, desc.ExpirationTime.IsZero())
	require.True(t, desc.LastAttemptCompleteTime.IsZero())
	require.True(t, desc.NextAttemptScheduleTime.IsZero())
}

func TestListActivitiesUnsetTimestampsAreZero(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	closed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	service.EXPECT().ListActivityExecutions(gomock.Any(), gomock.Any()).
		Return(&workflowservice.ListActivityExecutionsResponse{
			Executions: []*activitypb.ActivityExecutionListInfo{
				{ActivityId: "running", SearchAttributes: &commonpb.SearchAttributes{}},
				{ActivityId: "closed", CloseTime: timestamppb.New(closed), SearchAttributes: &commonpb.SearchAttributes{}},
			},
		}, nil)

	res, err := client.ListActivities(context.Background(), ClientListActivitiesOptions{})
	require.NoError(t, err)
	var got []*ClientActivityExecutionInfo
	for info, err := range res.Results {
		require.NoError(t, err)
		got = append(got, info)
	}
	require.Len(t, got, 2)
	require.True(t, got[0].CloseTime.IsZero())
	require.True(t, got[0].ExecutionTime.IsZero())
	require.Equal(t, closed, got[1].CloseTime)
}

func TestDescribeNexusOperationUnsetTimestampsAreZero(t *testing.T) {
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{})
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	service.EXPECT().DescribeNexusOperationExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeNexusOperationExecutionResponse{
			Info: &nexuspb.NexusOperationExecutionInfo{
				OperationId:      "op-id",
				RunId:            "run-id",
				CancellationInfo: &nexuspb.NexusOperationExecutionCancellationInfo{},
			},
		}, nil)

	handle := client.GetNexusOperationHandle(ClientGetNexusOperationHandleOptions{OperationID: "op-id", RunID: "run-id"})
	desc, err := handle.Describe(context.Background(), ClientDescribeNexusOperationOptions{})
	require.NoError(t, err)
	require.True(t, desc.ScheduledTime.IsZero())
	require.True(t, desc.CloseTime.IsZero())
	require.True(t, desc.ExpirationTime.IsZero())
	require.True(t, desc.LastAttemptCompleteTime.IsZero())
	require.True(t, desc.NextAttemptScheduleTime.IsZero())
	require.True(t, desc.CancellationInfo.RequestedTime.IsZero())
	require.True(t, desc.CancellationInfo.LastAttemptCompleteTime.IsZero())
	require.True(t, desc.CancellationInfo.NextAttemptScheduleTime.IsZero())
}
