package internal

import (
	"context"
	"fmt"
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

// stubBacklinkConverter installs a converter that turns a WorkflowEvent backlink into a nexus link
// whose URL references the workflow ID, mirroring the shape produced by the real temporalnexus
// converter. It restores the previous converter on test cleanup.
func stubBacklinkConverter(t *testing.T) {
	t.Helper()
	prev := workflowEventLinkToNexusLink
	t.Cleanup(func() { workflowEventLinkToNexusLink = prev })
	SetWorkflowEventLinkToNexusLinkConverter(func(link *commonpb.Link) (*nexuspb.Link, bool) {
		we := link.GetWorkflowEvent()
		if we == nil {
			return nil, false
		}
		return &nexuspb.Link{
			Url:  fmt.Sprintf("temporal:///namespaces/%s/workflows/%s/%s/history", we.GetNamespace(), we.GetWorkflowId(), we.GetRunId()),
			Type: string(we.ProtoReflect().Descriptor().FullName()),
		}, true
	})
}

// newBacklinkTestTaskHandler builds a nexusTaskHandler whose registered operation stashes a backlink
// on the operation context (simulating what a real handler does after issuing a signal) and then
// returns either a sync or async result.
func newBacklinkTestTaskHandler(t *testing.T, async bool) *nexusTaskHandler {
	t.Helper()
	backlink := workflowEventLink("callee-wf", "callee-run-id", enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)

	op := nexus.NewSyncOperation("operation", func(ctx context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
		nctx, _ := NexusOperationContextFromGoContext(ctx)
		nctx.AddResponseBacklink(backlink)
		return "result", nil
	})

	var nexusOp nexus.RegisterableOperation = op
	if async {
		nexusOp = &backlinkAsyncOperation{backlink: backlink}
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

// backlinkAsyncOperation stashes a backlink then returns an async result.
type backlinkAsyncOperation struct {
	nexus.UnimplementedOperation[string, string]
	backlink *commonpb.Link
}

func (o *backlinkAsyncOperation) Name() string { return "operation" }

func (o *backlinkAsyncOperation) Start(ctx context.Context, input string, _ nexus.StartOperationOptions) (nexus.HandlerStartOperationResult[string], error) {
	nctx, _ := NexusOperationContextFromGoContext(ctx)
	nctx.AddResponseBacklink(o.backlink)
	return &nexus.HandlerStartOperationResultAsync{OperationToken: "op-token"}, nil
}

func backlinkTestTask(t *testing.T, payload string) *workflowservice.PollNexusTaskQueueResponse {
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

// TestAsyncResponseIncludesSignalBacklinks verifies that signal-response backlinks stashed on the
// operation context during a handler invocation are merged into the resulting
// StartOperationResponse.Async. No server required.
func TestAsyncResponseIncludesSignalBacklinks(t *testing.T) {
	stubBacklinkConverter(t)
	h := newBacklinkTestTaskHandler(t, true)

	nctx, handlerErr := h.newNexusOperationContext(backlinkTestTask(t, "op-token"))
	require.Nil(t, handlerErr)
	completed, failed, err := h.ExecuteContext(nctx, backlinkTestTask(t, "op-token"))
	require.NoError(t, err)
	require.Nil(t, failed)

	async := completed.GetResponse().GetStartOperation().GetAsyncSuccess()
	require.NotNil(t, async)
	require.Equal(t, "op-token", async.GetOperationToken())
	require.Len(t, async.GetLinks(), 1)
	require.Contains(t, async.GetLinks()[0].GetUrl(), "callee-wf")
}

// TestSyncResponseIncludesSignalBacklinks is the sync mirror, guarding against the sync and async
// builders drifting (both must append the backlinks).
func TestSyncResponseIncludesSignalBacklinks(t *testing.T) {
	stubBacklinkConverter(t)
	h := newBacklinkTestTaskHandler(t, false)

	nctx, handlerErr := h.newNexusOperationContext(backlinkTestTask(t, "input"))
	require.Nil(t, handlerErr)
	completed, failed, err := h.ExecuteContext(nctx, backlinkTestTask(t, "input"))
	require.NoError(t, err)
	require.Nil(t, failed)

	sync := completed.GetResponse().GetStartOperation().GetSyncSuccess()
	require.NotNil(t, sync)
	require.Len(t, sync.GetLinks(), 1)
	require.Contains(t, sync.GetLinks()[0].GetUrl(), "callee-wf")
}

// TestResponseOmitsBacklinksWhenNoneStashed verifies the response carries no links when the handler
// issued no link-returning RPCs.
func TestResponseOmitsBacklinksWhenNoneStashed(t *testing.T) {
	stubBacklinkConverter(t)
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

	nctx, handlerErr := h.newNexusOperationContext(backlinkTestTask(t, "input"))
	require.Nil(t, handlerErr)
	completed, _, err := h.ExecuteContext(nctx, backlinkTestTask(t, "input"))
	require.NoError(t, err)
	require.Empty(t, completed.GetResponse().GetStartOperation().GetSyncSuccess().GetLinks())
}
