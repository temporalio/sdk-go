package internal

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
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
