package mocks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/client"
)

func Test_MockClient(t *testing.T) {
	testWorkflowID := "test-workflowid"
	testRunID := "test-runid"
	testWorkflowName := "workflow"
	testWorkflowInput := "input"
	mockClient := &Client{}

	mockWorkflowRun := &WorkflowRun{}
	mockWorkflowRun.On("GetID").Return(testWorkflowID).Times(4)
	mockWorkflowRun.On("GetRunID").Return(testRunID).Times(4)
	mockWorkflowRun.On("Get", mock.Anything, mock.Anything).Return(nil).Times(2)

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()
	wr, err := mockClient.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{}, testWorkflowName, testWorkflowInput)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, wr.GetID())
	require.Equal(t, testRunID, wr.GetRunID())
	require.NoError(t, mockWorkflowRun.Get(context.Background(), &testWorkflowID))

	mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()
	wr, err = mockClient.SignalWithStartWorkflow(context.Background(), "wid", "signal", "val", client.StartWorkflowOptions{}, testWorkflowName, testWorkflowInput)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, wr.GetID())
	require.Equal(t, testRunID, wr.GetRunID())

	mockClient.On("CancelWorkflow", mock.Anything, testWorkflowID, testRunID).Return(nil).Once()
	err = mockClient.CancelWorkflow(context.Background(), testWorkflowID, testRunID)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, wr.GetID())
	require.Equal(t, testRunID, wr.GetRunID())

	mockClient.On("GetWorkflow", mock.Anything, testWorkflowID, testRunID).Return(mockWorkflowRun).Once()
	wr = mockClient.GetWorkflow(context.Background(), testWorkflowID, testRunID)
	mockClient.AssertExpectations(t)
	require.Equal(t, testWorkflowID, wr.GetID())
	require.Equal(t, testRunID, wr.GetRunID())
	require.NoError(t, wr.Get(context.Background(), &testWorkflowID))

	mockHistoryIter := &HistoryEventIterator{}
	mockHistoryIter.On("HasNext").Return(true).Once()
	mockHistoryIter.On("Next").Return(&historypb.HistoryEvent{}, nil).Once()
	mockClient.On("GetWorkflowHistory", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockHistoryIter).Once()
	historyIter := mockClient.GetWorkflowHistory(context.Background(), testWorkflowID, testRunID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
	mockClient.AssertExpectations(t)
	mockWorkflowRun.AssertExpectations(t)

	require.NotNil(t, historyIter)
	require.Equal(t, true, historyIter.HasNext())
	next, err := historyIter.Next()
	require.NotNil(t, next)
	require.NoError(t, err)
}

func Test_MockResetWorkflowExecution(t *testing.T) {
	mockClient := &Client{}

	req := &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: "test-namespace",
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "wid",
			RunId:      "rid",
		},
		Reason:                    "bad deployment",
		WorkflowTaskFinishEventId: 6,
		RequestId:                 "request-id-random",
	}
	resp := &workflowservice.ResetWorkflowExecutionResponse{
		RunId: "new-run-id",
	}

	mockClient.On("ResetWorkflowExecution", mock.Anything, mock.Anything).Return(resp, nil).Once()
	actualResp, err := mockClient.ResetWorkflowExecution(context.Background(), req)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, "new-run-id", actualResp.GetRunId())
}

func Test_MockScheduleClient(t *testing.T) {
	testScheduleID := "test-scheduleID"
	mockClient := &ScheduleClient{}

	mockScheduleHandle := &ScheduleHandle{}
	mockScheduleHandle.On("GetID").Return(testScheduleID).Times(1)

	mockClient.On("Create", mock.Anything, mock.Anything).Return(mockScheduleHandle, nil).Once()
	wr, err := mockClient.Create(context.Background(), client.ScheduleOptions{})
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testScheduleID, wr.GetID())
}
