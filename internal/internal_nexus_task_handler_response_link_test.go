package internal

import (
	"context"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

// newResponseLinkTestTaskHandler builds a nexusTaskHandler whose registered operation stashes a
// response link on the operation context (simulating what a real handler does after issuing a
// signal) and then returns either a sync or async result.
func newResponseLinkTestTaskHandler(t *testing.T, async bool) *nexusTaskHandler {
	t.Helper()
	responseLink := workflowEventLink("callee-wf", "callee-run-id", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	op := nexus.NewSyncOperation("operation", func(ctx context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
		nctx, _ := NexusOperationContextFromGoContext(ctx)
		nctx.AddResponseLink(responseLink)
		return "result", nil
	})

	var nexusOp nexus.RegisterableOperation = op
	if async {
		nexusOp = &responseLinkAsyncOperation{responseLink: responseLink}
	}

	service := nexus.NewService("TestService")
	require.NoError(t, service.Register(nexusOp))

	reg := nexus.NewServiceRegistry()
	require.NoError(t, reg.Register(service))
	reg.Use(nexusMiddleware(nil))
	handler, err := reg.NewHandler()
	require.NoError(t, err)

	return newNexusTaskHandler(
		handler,
		"identity",
		signalLinkTestNamespace,
		"tq",
		nil, // client unused: handler doesn't issue a real RPC
		converter.GetDefaultDataConverter(),
		GetDefaultFailureConverter(),
		ilog.NewDefaultLogger(),
		metrics.NopHandler,
		newRegistry(),
	)
}

// responseLinkAsyncOperation stashes a response link then returns an async result.
type responseLinkAsyncOperation struct {
	nexus.UnimplementedOperation[string, string]
	responseLink *commonpb.Link
}

func (o *responseLinkAsyncOperation) Name() string { return "operation" }

func (o *responseLinkAsyncOperation) Start(ctx context.Context, input string, _ nexus.StartOperationOptions) (nexus.HandlerStartOperationResult[string], error) {
	nctx, _ := NexusOperationContextFromGoContext(ctx)
	nctx.AddResponseLink(o.responseLink)
	return &nexus.HandlerStartOperationResultAsync{OperationToken: "op-token"}, nil
}

func responseLinkTestTask(t *testing.T, payload string) *workflowservice.PollNexusTaskQueueResponse {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayload(payload)
	require.NoError(t, err)
	return &workflowservice.PollNexusTaskQueueResponse{
		TaskToken: []byte("token"),
		Request: &nexuspb.Request{
			Variant: &nexuspb.Request_StartOperation{
				StartOperation: &nexuspb.StartOperationRequest{
					Service:   "TestService",
					Operation: "operation",
					Payload:   p,
				},
			},
		},
	}
}

// TestAsyncResponseIncludesSignalResponseLinks verifies that signal response links stashed on the
// operation context during a handler invocation are merged into the resulting
// StartOperationResponse.Async. No server required.
func TestAsyncResponseIncludesSignalResponseLinks(t *testing.T) {
	h := newResponseLinkTestTaskHandler(t, true)

	nctx, handlerErr := h.newNexusOperationContext(responseLinkTestTask(t, "op-token"))
	require.Nil(t, handlerErr)
	completed, failed, err := h.ExecuteContext(nctx, responseLinkTestTask(t, "op-token"))
	require.NoError(t, err)
	require.Nil(t, failed)

	async := completed.GetResponse().GetStartOperation().GetAsyncSuccess()
	require.NotNil(t, async)
	require.Equal(t, "op-token", async.GetOperationToken())
	require.Len(t, async.GetLinks(), 1)
	require.Contains(t, async.GetLinks()[0].GetUrl(), "callee-wf")
}

// TestSyncResponseIncludesSignalResponseLinks is the sync mirror, guarding against the sync and async
// builders drifting (both must append the response links).
func TestSyncResponseIncludesSignalResponseLinks(t *testing.T) {
	h := newResponseLinkTestTaskHandler(t, false)

	nctx, handlerErr := h.newNexusOperationContext(responseLinkTestTask(t, "input"))
	require.Nil(t, handlerErr)
	completed, failed, err := h.ExecuteContext(nctx, responseLinkTestTask(t, "input"))
	require.NoError(t, err)
	require.Nil(t, failed)

	sync := completed.GetResponse().GetStartOperation().GetSyncSuccess()
	require.NotNil(t, sync)
	require.Len(t, sync.GetLinks(), 1)
	require.Contains(t, sync.GetLinks()[0].GetUrl(), "callee-wf")
}

// TestResponseOmitsResponseLinksWhenNoneStashed verifies the response carries no links when the
// handler issued no link-returning RPCs.
func TestResponseOmitsResponseLinksWhenNoneStashed(t *testing.T) {
	op := nexus.NewSyncOperation("operation", func(ctx context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
		return "result", nil
	})
	service := nexus.NewService("TestService")
	require.NoError(t, service.Register(op))
	reg := nexus.NewServiceRegistry()
	require.NoError(t, reg.Register(service))
	reg.Use(nexusMiddleware(nil))
	handler, err := reg.NewHandler()
	require.NoError(t, err)

	h := newNexusTaskHandler(handler, "identity", signalLinkTestNamespace, "tq", nil,
		converter.GetDefaultDataConverter(), GetDefaultFailureConverter(), ilog.NewDefaultLogger(),
		metrics.NopHandler, newRegistry())

	nctx, handlerErr := h.newNexusOperationContext(responseLinkTestTask(t, "input"))
	require.Nil(t, handlerErr)
	completed, _, err := h.ExecuteContext(nctx, responseLinkTestTask(t, "input"))
	require.NoError(t, err)
	require.Empty(t, completed.GetResponse().GetStartOperation().GetSyncSuccess().GetLinks())
}
