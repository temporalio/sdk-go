package internal

import (
	"context"
	"sync"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	failurepb "go.temporal.io/api/failure/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

// nexusCapturingDC captures which SerializationContext it receives.
type nexusCapturingDC struct {
	converter.DataConverter
	mu       sync.Mutex
	contexts []converter.SerializationContext
}

func (dc *nexusCapturingDC) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	dc.mu.Lock()
	dc.contexts = append(dc.contexts, ctx)
	dc.mu.Unlock()
	return dc
}

func (dc *nexusCapturingDC) getCaptured() []converter.SerializationContext {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	out := make([]converter.SerializationContext, len(dc.contexts))
	copy(out, dc.contexts)
	return out
}

// nexusCapturingFC captures which SerializationContext it receives.
type nexusCapturingFC struct {
	converter.FailureConverter
	mu       sync.Mutex
	contexts []converter.SerializationContext
}

func (fc *nexusCapturingFC) WithSerializationContext(ctx converter.SerializationContext) converter.FailureConverter {
	fc.mu.Lock()
	fc.contexts = append(fc.contexts, ctx)
	fc.mu.Unlock()
	return fc
}

func (fc *nexusCapturingFC) getCaptured() []converter.SerializationContext {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	out := make([]converter.SerializationContext, len(fc.contexts))
	copy(out, fc.contexts)
	return out
}

// nexusNoopFC is a minimal FailureConverter for tests.
type nexusNoopFC struct{}

func (nexusNoopFC) ErrorToFailure(err error) *failurepb.Failure {
	if err == nil {
		return nil
	}
	return &failurepb.Failure{Message: err.Error()}
}
func (nexusNoopFC) FailureToError(f *failurepb.Failure) error {
	if f == nil {
		return nil
	}
	return &ApplicationError{msg: f.GetMessage()}
}

func buildTestNexusTaskHandler(dc converter.DataConverter, fc converter.FailureConverter) *nexusTaskHandler {
	return &nexusTaskHandler{
		identity:         "test-identity",
		namespace:        "test-namespace",
		taskQueueName:    "test-tq",
		dataConverter:    dc,
		failureConverter: fc,
		logger:           ilog.NewNopLogger(),
		metricsHandler:   metrics.NopHandler,
		registry:         newRegistry(),
	}
}

func buildPollResponse(endpoint, service, operation string) *workflowservice.PollNexusTaskQueueResponse {
	return &workflowservice.PollNexusTaskQueueResponse{
		TaskToken: []byte("test-token"),
		Request: &nexuspb.Request{
			Endpoint: endpoint,
			Variant: &nexuspb.Request_StartOperation{
				StartOperation: &nexuspb.StartOperationRequest{
					Service:   service,
					Operation: operation,
					Payload:   nil,
				},
			},
		},
	}
}

func setupSyncHandler(t *testing.T, h *nexusTaskHandler, svc, op string, fn func(ctx context.Context, s string, opts nexus.StartOperationOptions) (string, error)) {
	t.Helper()
	syncOp := nexus.NewSyncOperation(op, fn)
	reg := nexus.NewServiceRegistry()
	service := nexus.NewService(svc)
	require.NoError(t, service.Register(syncOp))
	require.NoError(t, reg.Register(service))
	handler, err := reg.NewHandler()
	require.NoError(t, err)
	h.nexusHandler = handler
}

func TestNexusHandler_InputDeserializationUsesNexusContext(t *testing.T) {
	t.Parallel()
	capDC := &nexusCapturingDC{DataConverter: converter.GetDefaultDataConverter()}
	capFC := &nexusCapturingFC{FailureConverter: nexusNoopFC{}}
	h := buildTestNexusTaskHandler(capDC, capFC)

	setupSyncHandler(t, h, "my-service", "my-op", func(ctx context.Context, s string, opts nexus.StartOperationOptions) (string, error) {
		return "result", nil
	})

	payload, err := converter.GetDefaultDataConverter().ToPayload("hello")
	require.NoError(t, err)
	resp := buildPollResponse("my-endpoint", "my-service", "my-op")
	resp.Request.GetStartOperation().Payload = payload

	nctx, herr := h.newNexusOperationContext(resp)
	require.Nil(t, herr)
	require.Equal(t, "my-endpoint", nctx.Endpoint)
	require.Equal(t, "my-service", nctx.Service)
	require.Equal(t, "my-op", nctx.Operation)

	goCtx, cancel, herr := h.goContextForTask(nctx, nexus.Header{})
	require.Nil(t, herr)
	defer cancel()

	_, _, _ = h.handleStartOperation(goCtx, nctx, resp.Request.GetStartOperation(), nexus.Header{})

	captured := capDC.getCaptured()
	require.GreaterOrEqual(t, len(captured), 1)
	nexSC, ok := captured[0].(converter.NexusSerializationContext)
	require.True(t, ok, "expected NexusSerializationContext, got %T", captured[0])
	require.Equal(t, "test-namespace", nexSC.Namespace)
	require.Len(t, nexSC.Operations, 1)
	require.Equal(t, "my-endpoint", nexSC.Operations[0].Endpoint)
	require.Equal(t, "my-service", nexSC.Operations[0].Service)
	require.Equal(t, "my-op", nexSC.Operations[0].Operation)
}

func TestNexusHandler_SyncResultSerializationUsesNexusContext(t *testing.T) {
	t.Parallel()
	capDC := &nexusCapturingDC{DataConverter: converter.GetDefaultDataConverter()}
	h := buildTestNexusTaskHandler(capDC, nexusNoopFC{})

	setupSyncHandler(t, h, "my-service", "my-op", func(ctx context.Context, s string, opts nexus.StartOperationOptions) (string, error) {
		return "sync-result", nil
	})

	payload, err := converter.GetDefaultDataConverter().ToPayload("input-data")
	require.NoError(t, err)
	resp := buildPollResponse("ep-1", "my-service", "my-op")
	resp.Request.GetStartOperation().Payload = payload

	nctx, herr := h.newNexusOperationContext(resp)
	require.Nil(t, herr)

	goCtx, cancel, herr := h.goContextForTask(nctx, nexus.Header{})
	require.Nil(t, herr)
	defer cancel()

	res, handlerErr, taskErr := h.handleStartOperation(goCtx, nctx, resp.Request.GetStartOperation(), nexus.Header{})
	require.Nil(t, handlerErr)
	require.Nil(t, taskErr)

	syncResult := res.GetStartOperation().GetSyncSuccess()
	require.NotNil(t, syncResult)
	require.NotNil(t, syncResult.Payload)

	// dc was called with NexusSerializationContext for both input and output.
	captured := capDC.getCaptured()
	require.GreaterOrEqual(t, len(captured), 1)
	for _, c := range captured {
		nexSC, ok := c.(converter.NexusSerializationContext)
		require.True(t, ok, "expected NexusSerializationContext, got %T", c)
		require.Len(t, nexSC.Operations, 1)
		require.Equal(t, "ep-1", nexSC.Operations[0].Endpoint)
		require.Equal(t, "my-service", nexSC.Operations[0].Service)
		require.Equal(t, "my-op", nexSC.Operations[0].Operation)
	}
}

func TestNexusHandler_FailureConverterUsesNexusContext(t *testing.T) {
	t.Parallel()
	capFC := &nexusCapturingFC{FailureConverter: nexusNoopFC{}}
	h := buildTestNexusTaskHandler(converter.GetDefaultDataConverter(), capFC)

	failOp := nexus.NewSyncOperation("my-op", func(ctx context.Context, s string, opts nexus.StartOperationOptions) (string, error) {
		return "", &nexus.OperationError{
			State:   nexus.OperationStateFailed,
			Message: "op failed",
		}
	})
	reg := nexus.NewServiceRegistry()
	svc := nexus.NewService("my-service")
	require.NoError(t, svc.Register(failOp))
	require.NoError(t, reg.Register(svc))
	nexusHandler, err := reg.NewHandler()
	require.NoError(t, err)
	h.nexusHandler = nexusHandler

	payload, err := converter.GetDefaultDataConverter().ToPayload("input")
	require.NoError(t, err)
	resp := buildPollResponse("ep-2", "my-service", "my-op")
	resp.Request.GetStartOperation().Payload = payload

	nctx, herr := h.newNexusOperationContext(resp)
	require.Nil(t, herr)

	goCtx, cancel, herr := h.goContextForTask(nctx, nexus.Header{})
	require.Nil(t, herr)
	defer cancel()

	res, handlerErr, taskErr := h.handleStartOperation(goCtx, nctx, resp.Request.GetStartOperation(), nexus.Header{})
	require.Nil(t, handlerErr)
	require.Nil(t, taskErr)
	require.NotNil(t, res.GetStartOperation().GetFailure())

	captured := capFC.getCaptured()
	require.GreaterOrEqual(t, len(captured), 1)
	nexSC, ok := captured[0].(converter.NexusSerializationContext)
	require.True(t, ok, "expected NexusSerializationContext, got %T", captured[0])
	require.Len(t, nexSC.Operations, 1)
	require.Equal(t, "ep-2", nexSC.Operations[0].Endpoint)
	require.Equal(t, "my-service", nexSC.Operations[0].Service)
	require.Equal(t, "my-op", nexSC.Operations[0].Operation)
}

func TestNexusHandler_NoOpForVanillaConverter(t *testing.T) {
	t.Parallel()
	vanillaDC := converter.GetDefaultDataConverter()
	h := buildTestNexusTaskHandler(vanillaDC, nexusNoopFC{})

	setupSyncHandler(t, h, "my-service", "my-op", func(ctx context.Context, s string, opts nexus.StartOperationOptions) (string, error) {
		return "ok", nil
	})

	payload, err := vanillaDC.ToPayload("hello")
	require.NoError(t, err)
	resp := buildPollResponse("ep", "my-service", "my-op")
	resp.Request.GetStartOperation().Payload = payload

	nctx, herr := h.newNexusOperationContext(resp)
	require.Nil(t, herr)

	goCtx, cancel, herr := h.goContextForTask(nctx, nexus.Header{})
	require.Nil(t, herr)
	defer cancel()

	res, handlerErr, taskErr := h.handleStartOperation(goCtx, nctx, resp.Request.GetStartOperation(), nexus.Header{})
	require.Nil(t, handlerErr)
	require.Nil(t, taskErr)
	require.NotNil(t, res.GetStartOperation().GetSyncSuccess())

	var result string
	require.NoError(t, vanillaDC.FromPayload(res.GetStartOperation().GetSyncSuccess().Payload, &result))
	require.Equal(t, "ok", result)
}
