package internal

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	updatepb "go.temporal.io/api/update/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
)

// newSerCtxMockClient creates a mock service client and WorkflowClient with a capturing DC.
func newSerCtxMockClient(t *testing.T) (*gomock.Controller, *workflowservicemock.MockWorkflowServiceClient, *WorkflowClient, *serCtxCapturingDataConverter) {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

	dc := newSerCtxCapturingDataConverter()
	client := NewServiceClient(service, nil, ClientOptions{
		Namespace:     "test-namespace",
		DataConverter: dc,
	})
	return mockCtrl, service, client, dc
}

func TestClientStartWorkflow_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.StartWorkflowExecutionResponse{RunId: "run-1"}, nil)

	_, err := client.ExecuteWorkflow(context.Background(), StartWorkflowOptions{
		ID:        "wf-start-test",
		TaskQueue: "test-tq",
	}, "myWorkflow", "arg1")
	require.NoError(err)

	captured := dc.getCapturedContexts()
	require.NotEmpty(captured)

	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "wf-start-test" && wfCtx.Namespace == "test-namespace" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext with workflow ID wf-start-test")
}

func TestClientSignalWorkflow_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	service.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWorkflowExecutionResponse{}, nil)

	err := client.SignalWorkflow(context.Background(), "target-wf-signal", "", "my-signal", "data")
	require.NoError(err)

	captured := dc.getCapturedContexts()
	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "target-wf-signal" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext with target workflow ID")
}

func TestClientQueryWorkflow_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	queryResult, err := converter.GetDefaultDataConverter().ToPayloads("query-result")
	require.NoError(err)

	service.EXPECT().QueryWorkflow(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.QueryWorkflowResponse{QueryResult: queryResult}, nil)

	result, err := client.QueryWorkflow(context.Background(), "target-wf-query", "", "my-query")
	require.NoError(err)

	var str string
	require.NoError(result.Get(&str))
	require.Equal("query-result", str)

	captured := dc.getCapturedContexts()
	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "target-wf-query" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext for query")
}

func TestClientTerminateWorkflow_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	service.EXPECT().TerminateWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.TerminateWorkflowExecutionResponse{}, nil)

	err := client.TerminateWorkflow(context.Background(), "target-wf-terminate", "", "reason", "detail")
	require.NoError(err)

	captured := dc.getCapturedContexts()
	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "target-wf-terminate" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext for terminate")
}

func TestClientDescribeWorkflow_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	service.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Execution: &commonpb.WorkflowExecution{
					WorkflowId: "wf-describe-test",
					RunId:      "run-1",
				},
				SearchAttributes: &commonpb.SearchAttributes{},
			},
		}, nil)

	desc, err := client.DescribeWorkflow(context.Background(), "wf-describe-test", "run-1")
	require.NoError(err)
	require.NotNil(desc)

	captured := dc.getCapturedContexts()
	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "wf-describe-test" && wfCtx.Namespace == "test-namespace" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext for describe")
}

func TestClientPollWorkflowUpdate_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	resultPayloads, err := converter.GetDefaultDataConverter().ToPayloads("update-result")
	require.NoError(err)

	service.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.PollWorkflowExecutionUpdateResponse{
			Outcome: &updatepb.Outcome{
				Value: &updatepb.Outcome_Success{
					Success: resultPayloads,
				},
			},
		}, nil)

	ref := &updatepb.UpdateRef{
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "wf-poll-update-test",
			RunId:      "run-1",
		},
		UpdateId: "update-1",
	}
	output, err := client.PollWorkflowUpdate(context.Background(), ref)
	require.NoError(err)
	require.NotNil(output)

	captured := dc.getCapturedContexts()
	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "wf-poll-update-test" && wfCtx.Namespace == "test-namespace" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext for poll update")
}

func TestClientUpdateWithStartWorkflow_SerializationContext(t *testing.T) {
	require := require.New(t)
	mockCtrl, service, client, dc := newSerCtxMockClient(t)
	defer mockCtrl.Finish()

	service.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.ExecuteMultiOperationResponse{
			Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{
						StartWorkflow: &workflowservice.StartWorkflowExecutionResponse{
							RunId: "run-1",
						},
					},
				},
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{
						UpdateWorkflow: &workflowservice.UpdateWorkflowExecutionResponse{
							Stage: enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
						},
					},
				},
			},
		}, nil)

	startOp := client.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       "wf-update-with-start-test",
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                "test-tq",
		}, "myWorkflow",
	)

	_, err := client.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "my-update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	require.NoError(err)

	captured := dc.getCapturedContexts()
	found := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == "wf-update-with-start-test" && wfCtx.Namespace == "test-namespace" {
				found = true
				break
			}
		}
	}
	require.True(found, "should have captured WorkflowSerializationContext for update-with-start")
}
