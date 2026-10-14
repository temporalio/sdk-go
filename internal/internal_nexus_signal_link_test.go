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

// newSignalLinkTestClient builds the client and context a Nexus handler gets: a mock-backed client
// wrapped per invocation, and a context carrying the NexusOperationContext.
func newSignalLinkTestClient(t *testing.T) (*workflowservicemock.MockWorkflowServiceClient, *nexusOperationClient, context.Context, *NexusOperationContext) {
	t.Helper()
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().
		GetSystemInfo(gomock.Any(), gomock.Any()).
		AnyTimes().
		Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLinkTestNamespace})
	nctx := &NexusOperationContext{Namespace: signalLinkTestNamespace, TaskQueue: "tq"}
	ctx := context.WithValue(t.Context(), nexusOperationContextKey, nctx)
	return svc, &nexusOperationClient{Client: client}, ctx, nctx
}

// TestSignalForwardsRequestLinksAndCapturesResponseLink covers the happy path against a
// flag-enabled server: inbound nexus links are forwarded onto the SignalWorkflowExecutionRequest,
// and the response's response link is captured onto the operation context.
func TestSignalForwardsRequestLinksAndCapturesResponseLink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	requestLink := workflowEventLink("caller-wf", "caller-run", enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED)
	ctx = context.WithValue(ctx, NexusOperationRequestLinksKey, []*commonpb.Link{requestLink})

	responseLink := workflowEventLink(signalLinkTestWorkflowID, "target-run", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	var sent *workflowservice.SignalWorkflowExecutionRequest
	svc.EXPECT().
		SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWorkflowExecutionResponse{Link: responseLink}, nil
		})

	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "target-run", "test-signal", "payload"))

	// Forward direction: the request the SDK sent carries the inbound link.
	require.Len(t, sent.GetLinks(), 1)
	require.True(t, proto.Equal(sent.GetLinks()[0], requestLink))

	// Response direction: the response's link is captured onto the context.
	captured := nctx.ResponseLinks()
	require.Len(t, captured, 1)
	require.True(t, proto.Equal(captured[0], responseLink))
}

// TestSignalAgainstOlderServerCapturesNoResponseLink covers older-server compatibility: the server
// returns a response without a link. The SDK must not crash and must leave the response link list empty.
func TestSignalAgainstOlderServerCapturesNoResponseLink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	requestLink := workflowEventLink("caller-wf", "caller-run", enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED)
	ctx = context.WithValue(ctx, NexusOperationRequestLinksKey, []*commonpb.Link{requestLink})

	var sent *workflowservice.SignalWorkflowExecutionRequest
	svc.EXPECT().
		SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWorkflowExecutionResponse, error) {
			sent = req
			// Pre-1.31 server / flag-off server: response has no link.
			return &workflowservice.SignalWorkflowExecutionResponse{}, nil
		})

	require.NoError(t, client.SignalWorkflow(ctx, signalLinkTestWorkflowID, "target-run", "test-signal", "payload"))

	// Forward direction still works regardless of server version.
	require.Len(t, sent.GetLinks(), 1)
	// Response direction: no response link captured because the server didn't send one.
	require.Empty(t, nctx.ResponseLinks())
}

// TestSignalWithStartForwardsRequestLinksAndCapturesResponseLink mirrors the plain-signal happy
// path but for signalWithStart, which uses a different proto field (signal_link) and a different
// code path inside the client.
func TestSignalWithStartForwardsRequestLinksAndCapturesResponseLink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	requestLink := workflowEventLink("caller-wf", "caller-run", enumspb.EVENT_TYPE_NEXUS_OPERATION_SCHEDULED)
	ctx = context.WithValue(ctx, NexusOperationRequestLinksKey, []*commonpb.Link{requestLink})

	responseLink := workflowEventLink(signalLinkTestWorkflowID, "target-run", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	var sent *workflowservice.SignalWithStartWorkflowExecutionRequest
	svc.EXPECT().
		SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run", SignalLink: responseLink}, nil
		})

	_, err := client.SignalWithStartWorkflow(ctx, signalLinkTestWorkflowID, "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)

	// Forward direction: the SignalWithStartWorkflowExecutionRequest carries the inbound link.
	require.Len(t, sent.GetLinks(), 1)
	require.True(t, proto.Equal(sent.GetLinks()[0], requestLink))

	// Response direction: response.signal_link is captured onto the context.
	captured := nctx.ResponseLinks()
	require.Len(t, captured, 1)
	require.True(t, proto.Equal(captured[0], responseLink))
}

// TestSignalWithStartAgainstOlderServerCapturesNoResponseLink: older server omits signal_link.
func TestSignalWithStartAgainstOlderServerCapturesNoResponseLink(t *testing.T) {
	svc, client, ctx, nctx := newSignalLinkTestClient(t)

	svc.EXPECT().
		SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run"}, nil)

	_, err := client.SignalWithStartWorkflow(ctx, signalLinkTestWorkflowID, "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)
	require.Empty(t, nctx.ResponseLinks())
}

// TestSignalOutsideNexusContextCapturesNoResponseLink: a plain signal not issued from a Nexus operation
// context must not attempt to capture a response link even if the server returns one.
func TestSignalOutsideNexusContextCapturesNoResponseLink(t *testing.T) {
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any()).AnyTimes().Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLinkTestNamespace})

	responseLink := workflowEventLink(signalLinkTestWorkflowID, "target-run", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)
	svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWorkflowExecutionResponse{Link: responseLink}, nil)

	// No NexusOperationContext on the context, so this must not panic.
	require.NoError(t, client.SignalWorkflow(t.Context(), signalLinkTestWorkflowID, "target-run", "test-signal", "payload"))
}
