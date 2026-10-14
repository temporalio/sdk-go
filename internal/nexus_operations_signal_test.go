package internal

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
)

const (
	signalLimitTestRequestID  = "nexus-op-request-id"
	signalLimitTestNamespace  = "test-namespace"
	signalLimitTestWorkflowID = "wf-target"
)

// newSignalLimitTestClient wraps a mock-backed client the way the Nexus task handler wraps the
// worker's client, and seeds the operation's request ID onto the context.
func newSignalLimitTestClient(t *testing.T) (*workflowservicemock.MockWorkflowServiceClient, *nexusOperationClient, context.Context) {
	t.Helper()
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any()).AnyTimes().Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLimitTestNamespace})
	ctx := context.WithValue(t.Context(), NexusOperationRequestIDKey, signalLimitTestRequestID)
	return svc, &nexusOperationClient{Client: client}, ctx
}

// TestSignalUsesOperationRequestID: a signal issued from a handler carries the operation's request
// ID rather than a fresh UUID, so a retried Nexus task dedupes against the first attempt.
func TestSignalUsesOperationRequestID(t *testing.T) {
	svc, client, ctx := newSignalLimitTestClient(t)

	var sent *workflowservice.SignalWorkflowExecutionRequest
	svc.EXPECT().
		SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWorkflowExecutionResponse{}, nil
		})

	require.NoError(t, client.SignalWorkflow(ctx, signalLimitTestWorkflowID, "", "test-signal", "payload"))
	require.Equal(t, signalLimitTestRequestID, sent.GetRequestId())
}

// TestSignalWithStartUsesOperationRequestID: same for signalWithStart, which inside a Nexus handler
// uses the operation's request ID instead of generating one.
func TestSignalWithStartUsesOperationRequestID(t *testing.T) {
	svc, client, ctx := newSignalLimitTestClient(t)

	var sent *workflowservice.SignalWithStartWorkflowExecutionRequest
	svc.EXPECT().
		SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run"}, nil
		})

	_, err := client.SignalWithStartWorkflow(ctx, signalLimitTestWorkflowID, "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)
	require.Equal(t, signalLimitTestRequestID, sent.GetRequestId())
}

// TestSignalWithStartOutsideNexusContextGeneratesRequestID: outside a Nexus handler there is no
// operation request ID, so signalWithStart keeps generating its own.
func TestSignalWithStartOutsideNexusContextGeneratesRequestID(t *testing.T) {
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any()).AnyTimes().Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLimitTestNamespace})

	var sent *workflowservice.SignalWithStartWorkflowExecutionRequest
	svc.EXPECT().
		SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
			sent = req
			return &workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run"}, nil
		})

	_, err := client.SignalWithStartWorkflow(t.Context(), signalLimitTestWorkflowID, "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	require.NoError(t, err)
	_, err = uuid.Parse(sent.GetRequestId())
	require.NoError(t, err)
}

// TestOnlyOneSignalPerInvocation: every signal in an invocation carries the operation's request ID,
// so a second one could be deduped against the first and reported as delivered without reaching the
// workflow. Any second signal or signalWithStart is rejected, whatever workflow it targets.
func TestOnlyOneSignalPerInvocation(t *testing.T) {
	signal := func(workflowID string) func(context.Context, *nexusOperationClient) error {
		return func(ctx context.Context, c *nexusOperationClient) error {
			return c.SignalWorkflow(ctx, workflowID, "", "test-signal", "payload")
		}
	}
	signalWithStart := func(workflowID string) func(context.Context, *nexusOperationClient) error {
		return func(ctx context.Context, c *nexusOperationClient) error {
			_, err := c.SignalWithStartWorkflow(ctx, workflowID, "test-signal", "signal-payload",
				StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
			return err
		}
	}
	for name, tc := range map[string]struct {
		first, second func(context.Context, *nexusOperationClient) error
	}{
		"same workflow":                  {signal(signalLimitTestWorkflowID), signal(signalLimitTestWorkflowID)},
		"different workflow":             {signal(signalLimitTestWorkflowID), signal("other-wf")},
		"signal then signalWithStart":    {signal(signalLimitTestWorkflowID), signalWithStart(signalLimitTestWorkflowID)},
		"signalWithStart then signal":    {signalWithStart(signalLimitTestWorkflowID), signal(signalLimitTestWorkflowID)},
		"empty-ID signalWithStart twice": {signalWithStart(""), signalWithStart("")},
	} {
		t.Run(name, func(t *testing.T) {
			svc, client, ctx := newSignalLimitTestClient(t)
			svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).MaxTimes(1).
				Return(&workflowservice.SignalWorkflowExecutionResponse{}, nil)
			svc.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).MaxTimes(1).
				Return(&workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run"}, nil)

			require.NoError(t, tc.first(ctx, client))
			err := tc.second(ctx, client)
			var handlerErr *nexus.HandlerError
			require.ErrorAs(t, err, &handlerErr)
			require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
			require.ErrorContains(t, err, "only one signal can be sent per Nexus operation invocation")
		})
	}
}

// TestFailedSignalDoesNotConsumeSlot: a send that errored may be retried within the same
// invocation, mirroring the async-start guard. Safe because the retry reuses the request ID.
func TestFailedSignalDoesNotConsumeSlot(t *testing.T) {
	svc, client, ctx := newSignalLimitTestClient(t)

	gomock.InOrder(
		svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(nil, serviceerror.NewUnavailable("try again")),
		svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(&workflowservice.SignalWorkflowExecutionResponse{}, nil),
	)

	require.Error(t, client.SignalWorkflow(ctx, signalLimitTestWorkflowID, "", "test-signal", "payload"))
	require.NoError(t, client.SignalWorkflow(ctx, signalLimitTestWorkflowID, "", "test-signal", "payload"))
}
