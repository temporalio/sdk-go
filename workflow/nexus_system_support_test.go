package workflow_test

import (
	"context"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const nexusSystemDataConverterMarker = "nexus-system-test"

type nexusSystemMarkerDataConverter struct {
	converter.DataConverter
}

func (c nexusSystemMarkerDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := c.DataConverter.ToPayload(value)
	if err != nil {
		return nil, err
	}
	if payload.Metadata == nil {
		payload.Metadata = map[string][]byte{}
	}
	payload.Metadata["test-dc"] = []byte(nexusSystemDataConverterMarker)
	return payload, nil
}

func (c nexusSystemMarkerDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	payloads := &commonpb.Payloads{Payloads: make([]*commonpb.Payload, 0, len(values))}
	for _, value := range values {
		payload, err := c.ToPayload(value)
		if err != nil {
			return nil, err
		}
		payloads.Payloads = append(payloads.Payloads, payload)
	}
	return payloads, nil
}

type nexusSystemOperationTrace struct {
	endpoint  string
	service   string
	operation any
	input     any
}

func targetWorkflow(_ string, input string) string {
	return input
}

type nexusSystemTracingInterceptor struct {
	interceptor.WorkerInterceptorBase
	traces *[]nexusSystemOperationTrace
}

func (i nexusSystemTracingInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	return &nexusSystemTracingWorkflowInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
		traces:                         i.traces,
	}
}

type nexusSystemTracingWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	traces *[]nexusSystemOperationTrace
}

func (i *nexusSystemTracingWorkflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return i.Next.Init(&nexusSystemTracingWorkflowOutboundInterceptor{
		WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: outbound},
		traces:                          i.traces,
	})
}

type nexusSystemTracingWorkflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	traces *[]nexusSystemOperationTrace
}

func (i *nexusSystemTracingWorkflowOutboundInterceptor) ExecuteNexusOperation(
	ctx workflow.Context,
	input interceptor.ExecuteNexusOperationInput,
) workflow.NexusOperationFuture {
	*i.traces = append(*i.traces, nexusSystemOperationTrace{
		endpoint:  input.Client.Endpoint(),
		service:   input.Client.Service(),
		operation: input.Operation,
		input:     input.Input,
	})
	return i.Next.ExecuteNexusOperation(ctx, input)
}

func TestSystemNexusSignalWithStartUsesConverters(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetDataConverter(nexusSystemMarkerDataConverter{DataConverter: converter.GetDefaultDataConverter()})
	var traces []nexusSystemOperationTrace
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{
			&nexusSystemTracingInterceptor{traces: &traces},
		},
	})

	var captured *workflowservicepb.SignalWithStartWorkflowExecutionRequest
	op := nexus.NewSyncOperation(
		"SignalWithStartWorkflowExecution",
		func(
			ctx context.Context,
			input *workflowservicepb.SignalWithStartWorkflowExecutionRequest,
			opts nexus.StartOperationOptions,
		) (*workflowservicepb.SignalWithStartWorkflowExecutionResponse, error) {
			captured = input
			return &workflowservicepb.SignalWithStartWorkflowExecutionResponse{}, nil
		},
	)
	service := nexus.NewService("temporal.api.workflowservice.v1.WorkflowService")
	require.NoError(t, service.Register(op))
	env.RegisterNexusService(service)

	searchAttributeKey := temporal.NewSearchAttributeKeyKeyword("CustomKeyword")
	searchAttributes := temporal.NewSearchAttributes(searchAttributeKey.ValueSet("search-value"))

	env.ExecuteWorkflow(func(ctx workflow.Context) (*workflow.SignalWithStartWorkflowResponse, error) {
		summary := "summary"
		details := "details"
		fut := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
			Id:                 "workflow-id",
			Memo:               map[string]any{"memo-key": "memo-value"},
			SearchAttributes:   searchAttributes,
			VersioningOverride: &client.AutoUpgradeVersioningOverride{},
			UserMetadata: workflow.UserMetadata{
				StaticSummary: summary,
				StaticDetails: details,
			},
		}, "signal-name", "signal-arg", targetWorkflow, "workflow-arg")
		var result workflow.SignalWithStartWorkflowResponse
		var resultErr error
		workflow.NewSelector(ctx).AddFuture(fut, func(ready workflow.Future) {
			resultErr = ready.Get(ctx, &result)
		}).Select(ctx)
		if resultErr != nil {
			return nil, resultErr
		}
		return &result, nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, captured)
	require.Equal(t, "default-test-namespace", captured.GetNamespace())
	require.Equal(t, "default-test-taskqueue", captured.GetTaskQueue().GetName())
	require.Nil(t, captured.GetWorkflowExecutionTimeout())
	require.Nil(t, captured.GetWorkflowRunTimeout())
	require.Nil(t, captured.GetWorkflowTaskTimeout())
	require.Nil(t, captured.GetRetryPolicy())
	require.Nil(t, captured.GetPriority())

	requirePayloadMarked(t, captured.GetInput().GetPayloads()[0])
	requirePayloadMarked(t, captured.GetSignalInput().GetPayloads()[0])
	requirePayloadMarked(t, captured.GetMemo().GetFields()["memo-key"])
	requirePayloadMarked(t, captured.GetUserMetadata().GetSummary())
	requirePayloadMarked(t, captured.GetUserMetadata().GetDetails())

	searchPayload := captured.GetSearchAttributes().GetIndexedFields()["CustomKeyword"]
	require.NotNil(t, searchPayload)
	require.Equal(t, []byte("Keyword"), searchPayload.GetMetadata()["type"])
	require.Empty(t, searchPayload.GetMetadata()["test-dc"])

	require.NotNil(t, captured.GetVersioningOverride())
	_, ok := captured.GetVersioningOverride().GetOverride().(*workflowpb.VersioningOverride_AutoUpgrade)
	require.True(t, ok)

	require.Len(t, traces, 1)
	require.Equal(t, "__temporal_system", traces[0].endpoint)
	require.Equal(t, "temporal.api.workflowservice.v1.WorkflowService", traces[0].service)
	require.Equal(t, "SignalWithStartWorkflowExecution", traces[0].operation)
	traceRequest, ok := traces[0].input.(*workflowservicepb.SignalWithStartWorkflowExecutionRequest)
	require.True(t, ok)
	require.Equal(t, "workflow-id", traceRequest.GetWorkflowId())
	require.Equal(t, "signal-name", traceRequest.GetSignalName())
}

func requirePayloadMarked(t *testing.T, payload *commonpb.Payload) {
	t.Helper()
	require.NotNil(t, payload)
	require.Equal(t, []byte(nexusSystemDataConverterMarker), payload.GetMetadata()["test-dc"])
}
