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

// newSignalLimitTestClient wraps a mock-backed client the way temporalOperation.Start wraps the
// client it hands to a Temporal operation handler, carrying the operation's request ID.
func newSignalLimitTestClient(t *testing.T) (*workflowservicemock.MockWorkflowServiceClient, *temporalOperationClient, context.Context) {
	t.Helper()
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	svc.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any()).AnyTimes().Return(&workflowservice.GetSystemInfoResponse{}, nil)
	client := NewServiceClient(svc, nil, ClientOptions{Namespace: signalLimitTestNamespace})
	return svc, &temporalOperationClient{Client: client, requestID: signalLimitTestRequestID}, t.Context()
}

// sendSignal and sendSignalWithStart are the two ways to signal, in one shape so a test can cover
// both.
func sendSignal(workflowID string) func(context.Context, Client) error {
	return func(ctx context.Context, c Client) error {
		return c.SignalWorkflow(ctx, workflowID, "", "test-signal", "payload")
	}
}

func sendSignalWithStart(workflowID string) func(context.Context, Client) error {
	return func(ctx context.Context, c Client) error {
		_, err := c.SignalWithStartWorkflow(ctx, workflowID, "test-signal", "signal-payload",
			StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
		return err
	}
}

// expectSignalRequestID allows one call to either signal RPC and returns a pointer to the request
// ID it carried.
func expectSignalRequestID(svc *workflowservicemock.MockWorkflowServiceClient) *string {
	var requestID string
	svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).MaxTimes(1).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWorkflowExecutionResponse, error) {
			requestID = req.GetRequestId()
			return &workflowservice.SignalWorkflowExecutionResponse{}, nil
		})
	svc.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).MaxTimes(1).
		DoAndReturn(func(_ context.Context, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...any) (*workflowservice.SignalWithStartWorkflowExecutionResponse, error) {
			requestID = req.GetRequestId()
			return &workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: "target-run"}, nil
		})
	return &requestID
}

// TestSignalsUseOperationRequestID: a signal sent through the wrapped client carries the
// operation's request ID rather than a fresh UUID, so a retried Nexus task dedupes against the
// first attempt.
func TestSignalsUseOperationRequestID(t *testing.T) {
	for name, send := range map[string]func(context.Context, Client) error{
		"signal":          sendSignal(signalLimitTestWorkflowID),
		"signalWithStart": sendSignalWithStart(signalLimitTestWorkflowID),
	} {
		t.Run(name, func(t *testing.T) {
			svc, client, ctx := newSignalLimitTestClient(t)
			requestID := expectSignalRequestID(svc)
			require.NoError(t, send(ctx, client))
			require.Equal(t, signalLimitTestRequestID, *requestID)
		})
	}
}

// TestUnwrappedClientSignalsGenerateRequestID: only the wrapper supplies an operation request
// ID, so an unwrapped client keeps generating its own.
func TestUnwrappedClientSignalsGenerateRequestID(t *testing.T) {
	for name, send := range map[string]func(context.Context, Client) error{
		"signal":          sendSignal(signalLimitTestWorkflowID),
		"signalWithStart": sendSignalWithStart(signalLimitTestWorkflowID),
	} {
		t.Run(name, func(t *testing.T) {
			svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			svc.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any()).AnyTimes().Return(&workflowservice.GetSystemInfoResponse{}, nil)
			requestID := expectSignalRequestID(svc)

			require.NoError(t, send(t.Context(), NewServiceClient(svc, nil, ClientOptions{Namespace: signalLimitTestNamespace})))
			_, err := uuid.Parse(*requestID)
			require.NoError(t, err)
		})
	}
}

// TestOnlyOneSignalPerInvocation: every signal in an invocation carries the operation's request ID,
// so a second one could be deduped against the first and reported as delivered without reaching the
// workflow. Any second signal or signalWithStart is rejected, whatever workflow it targets.
func TestOnlyOneSignalPerInvocation(t *testing.T) {
	for name, tc := range map[string]struct {
		first, second func(context.Context, Client) error
	}{
		"same workflow":               {sendSignal(signalLimitTestWorkflowID), sendSignal(signalLimitTestWorkflowID)},
		"different workflow":          {sendSignal(signalLimitTestWorkflowID), sendSignal("other-wf")},
		"signal then signalWithStart": {sendSignal(signalLimitTestWorkflowID), sendSignalWithStart(signalLimitTestWorkflowID)},
		"signalWithStart then signal": {sendSignalWithStart(signalLimitTestWorkflowID), sendSignal(signalLimitTestWorkflowID)},
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

// TestSignalWithStartRequiresWorkflowID: an empty ID is defaulted to a fresh UUID per call, so a
// retried Nexus task would start a second workflow rather than dedupe against the first.
func TestSignalWithStartRequiresWorkflowID(t *testing.T) {
	svc, client, ctx := newSignalLimitTestClient(t)
	svc.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any()).Times(0)

	_, err := client.SignalWithStartWorkflow(ctx, "", "test-signal", "signal-payload",
		StartWorkflowOptions{TaskQueue: "tq"}, "TestWorkflow")
	var handlerErr *nexus.HandlerError
	require.ErrorAs(t, err, &handlerErr)
	require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
	require.ErrorContains(t, err, "requires a workflow ID")

	// The rejected call must not consume the invocation's single signal slot.
	svc.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any()).
		Return(&workflowservice.SignalWorkflowExecutionResponse{}, nil)
	require.NoError(t, client.SignalWorkflow(ctx, signalLimitTestWorkflowID, "", "test-signal", "payload"))
}
