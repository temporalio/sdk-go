package internal

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
)

const (
	signalLinkTestNamespace  = "test-namespace"
	signalLinkTestWorkflowID = "wf-target"
)

// workflowEventLink builds a common.v1.Link with a WorkflowEvent variant for use in the tests.
func workflowEventLink(workflowID, runID string, eventType enumspb.EventType) *commonpb.Link {
	return &commonpb.Link{
		Variant: &commonpb.Link_WorkflowEvent_{
			WorkflowEvent: &commonpb.Link_WorkflowEvent{
				Namespace:  signalLinkTestNamespace,
				WorkflowId: workflowID,
				RunId:      runID,
				Reference: &commonpb.Link_WorkflowEvent_EventRef{
					EventRef: &commonpb.Link_WorkflowEvent_EventReference{
						EventType: eventType,
					},
				},
			},
		},
	}
}

// newSignalLinkTestClient builds a WorkflowClient backed by a mock service, plus a context carrying
// a NexusOperationContext, mirroring what the Nexus task handler sets up before invoking a handler.
func newSignalLinkTestClient(t *testing.T) (*workflowservicemock.MockWorkflowServiceClient, *WorkflowClient, context.Context, *NexusOperationContext) {
	t.Helper()
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().
		GetSystemInfo(gomock.Any(), gomock.Any()).
		AnyTimes().
		Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLinkTestNamespace})
	nctx := &NexusOperationContext{Namespace: signalLinkTestNamespace, TaskQueue: "tq"}
	ctx := context.WithValue(context.Background(), nexusOperationContextKey, nctx)
	return svc, client, ctx, nctx
}

// TestSignalForwardsInboundLinksAndCapturesResponseBacklink covers the happy path against a
// flag-enabled server: inbound nexus links are forwarded onto the SignalWorkflowExecutionRequest,
// and the response's backlink is captured onto the operation context.
func TestSignalForwardsInboundLinksAndCapturesResponseBacklink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	inboundLink := workflowEventLink("caller-wf", "caller-run", enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED)
	ctx = context.WithValue(ctx, NexusOperationLinksKey, []*commonpb.Link{inboundLink})

	responseLink := workflowEventLink(signalLinkTestWorkflowID, "target-run", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	var sent *workflowservice.SignalWorkflowExecutionRequest
	svc.EXPECT().
		SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWorkflowExecutionRequest, _ ...interface{}) (*workflowservice.SignalWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWorkflowExecutionResponse{Link: responseLink}, nil
		})

	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "target-run", "test-signal", "payload"))

	// Forward direction: the request the SDK sent carries the inbound link.
	require.Len(t, sent.GetLinks(), 1)
	require.True(t, proto.Equal(sent.GetLinks()[0], inboundLink))

	// Backward direction: the response's link is captured onto the context.
	captured := nctx.ResponseBacklinks()
	require.Len(t, captured, 1)
	require.True(t, proto.Equal(captured[0], responseLink))
}

// TestSignalAgainstOlderServerCapturesNoBacklink covers older-server compatibility: the server
// returns a response without a link. The SDK must not crash and must leave the backlink list empty.
func TestSignalAgainstOlderServerCapturesNoBacklink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	inboundLink := workflowEventLink("caller-wf", "caller-run", enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED)
	ctx = context.WithValue(ctx, NexusOperationLinksKey, []*commonpb.Link{inboundLink})

	var sent *workflowservice.SignalWorkflowExecutionRequest
	svc.EXPECT().
		SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWorkflowExecutionRequest, _ ...interface{}) (*workflowservice.SignalWorkflowExecutionResponse, error) {
			sent = req
			// Pre-1.31 server / flag-off server: response has no link.
			return &workflowservice.SignalWorkflowExecutionResponse{}, nil
		})

	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "target-run", "test-signal", "payload"))

	// Forward direction still works regardless of server version.
	require.Len(t, sent.GetLinks(), 1)
	// Backward direction: no backlink captured because the server didn't send one.
	require.Empty(t, nctx.ResponseBacklinks())
}

// TestMultipleSignalsAccumulateAllBacklinks: two signal RPCs in a row each contribute a backlink;
// both must be captured in order on the context.
func TestMultipleSignalsAccumulateAllBacklinks(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	firstResponseLink := workflowEventLink("callee-a", "run-a", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)
	secondResponseLink := workflowEventLink("callee-b", "run-b", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)
	gomock.InOrder(
		svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(&workflowservice.SignalWorkflowExecutionResponse{Link: firstResponseLink}, nil),
		svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(&workflowservice.SignalWorkflowExecutionResponse{Link: secondResponseLink}, nil),
	)

	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "run-a", "test-signal", "payload"))
	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "run-b", "test-signal", "payload"))

	captured := nctx.ResponseBacklinks()
	require.Len(t, captured, 2)
	require.True(t, proto.Equal(captured[0], firstResponseLink))
	require.True(t, proto.Equal(captured[1], secondResponseLink))
}

// TestSignalWithStartForwardsInboundLinksAndCapturesResponseBacklink mirrors the plain-signal happy
// path but for signalWithStart, which uses a different proto field (signal_link) and a different
// code path inside the client.
func TestSignalWithStartForwardsInboundLinksAndCapturesResponseBacklink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	inboundLink := workflowEventLink("caller-wf", "caller-run", enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED)
	ctx = context.WithValue(ctx, NexusOperationLinksKey, []*commonpb.Link{inboundLink})

	responseLink := workflowEventLink(signalLinkTestWorkflowID, "target-run", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	var sent *workflowservice.SignalWithStartWorkflowExecutionRequest
	svc.EXPECT().
		SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...interface{}) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run", SignalLink: responseLink}, nil
		})

	_, err := client.SignalWithStartWorkflow(ctx, signalLinkTestWorkflowID, "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)

	// Forward direction: the SignalWithStartWorkflowExecutionRequest carries the inbound link.
	require.Len(t, sent.GetLinks(), 1)
	require.True(t, proto.Equal(sent.GetLinks()[0], inboundLink))

	// Backward direction: response.signal_link is captured onto the context.
	captured := nctx.ResponseBacklinks()
	require.Len(t, captured, 1)
	require.True(t, proto.Equal(captured[0], responseLink))
}

// TestSignalWithStartAgainstOlderServerCapturesNoBacklink: older server omits signal_link.
func TestSignalWithStartAgainstOlderServerCapturesNoBacklink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	svc.EXPECT().
		SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run"}, nil)

	_, err := client.SignalWithStartWorkflow(ctx, signalLinkTestWorkflowID, "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)
	require.Empty(t, nctx.ResponseBacklinks())
}

// TestMixedSignalAndSignalWithStartAccumulateAllBacklinks: a handler that issues one signal and one
// signalWithStart against the same context must end up with both backlinks captured, in call order.
func TestMixedSignalAndSignalWithStartAccumulateAllBacklinks(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	signalResponseLink := workflowEventLink("callee-s", "run-s", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)
	signalWithStartResponseLink := workflowEventLink("callee-sws", "run-sws", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWorkflowExecutionResponse{Link: signalResponseLink}, nil)
	svc.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "run-sws", SignalLink: signalWithStartResponseLink}, nil)

	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "run-s", "test-signal", "payload"))
	_, err := client.SignalWithStartWorkflow(ctx, "callee-sws", "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)

	captured := nctx.ResponseBacklinks()
	require.Len(t, captured, 2)
	require.True(t, proto.Equal(captured[0], signalResponseLink))
	require.True(t, proto.Equal(captured[1], signalWithStartResponseLink))
}

// TestSignalOutsideNexusContextCapturesNoBacklink: a plain signal not issued from a Nexus operation
// context must not attempt to capture a backlink even if the server returns one.
func TestSignalOutsideNexusContextCapturesNoBacklink(t *testing.T) {
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any()).AnyTimes().Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLinkTestNamespace})

	responseLink := workflowEventLink(signalLinkTestWorkflowID, "target-run", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)
	svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWorkflowExecutionResponse{Link: responseLink}, nil)

	// No NexusOperationContext on the context, so this must not panic.
	require.NoError(t, client.SignalWorkflow(context.Background(), signalLinkTestWorkflowID, "target-run", "test-signal", "payload"))
}
