package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

type nexusContextCheckedConverter struct {
	converter.DataConverter
	serializationContext converter.SerializationContext
	ctx                  context.Context
	checkInput           bool
	checkResult          bool
}

func (dc nexusContextCheckedConverter) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	dc.serializationContext = ctx
	dc.ctx = nil
	return dc
}

func (dc nexusContextCheckedConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.ctx = ctx
	return dc
}

func (dc nexusContextCheckedConverter) WithWorkflowContext(Context) converter.DataConverter {
	return dc
}

func (dc nexusContextCheckedConverter) checkContext() error {
	expected := converter.NexusSerializationContext{
		Endpoint: "handler-endpoint", Service: "handler-service", Operation: "handler-operation",
	}
	if dc.serializationContext != expected {
		return fmt.Errorf("missing Nexus serialization context")
	}
	if dc.ctx == nil {
		return fmt.Errorf("missing Go converter context after serialization context binding")
	}
	nctx, ok := NexusOperationContextFromGoContext(dc.ctx)
	if !ok || nctx.Endpoint != expected.Endpoint || nctx.RequestID != "request-id" {
		return fmt.Errorf("missing Nexus operation context or request ID")
	}
	if !nexus.IsHandlerContext(dc.ctx) {
		return fmt.Errorf("missing Nexus handler context")
	}
	info := nexus.ExtractHandlerInfo(dc.ctx)
	if info.Service != expected.Service || info.Operation != expected.Operation || info.Header.Get("test-header") != "header-value" {
		return fmt.Errorf("missing Nexus handler metadata")
	}
	if _, ok := dc.ctx.Deadline(); !ok {
		return fmt.Errorf("missing Nexus request timeout")
	}
	return nil
}

func (dc nexusContextCheckedConverter) FromPayload(payload *commonpb.Payload, value any) error {
	if dc.checkInput {
		if err := dc.checkContext(); err != nil {
			return err
		}
	}
	return dc.DataConverter.FromPayload(payload, value)
}

func (dc nexusContextCheckedConverter) ToPayload(value any) (*commonpb.Payload, error) {
	if dc.checkResult {
		if err := dc.checkContext(); err != nil {
			return nil, err
		}
	}
	return dc.DataConverter.ToPayload(value)
}

func TestConverterContext_NexusTaskHandler(t *testing.T) {
	for _, branch := range []string{"input", "sync-result"} {
		t.Run(branch, func(t *testing.T) {
			dc := nexusContextCheckedConverter{
				DataConverter: converter.GetDefaultDataConverter(),
				checkInput:    branch == "input",
				checkResult:   branch == "sync-result",
			}
			inputPayload, err := converter.GetDefaultDataConverter().ToPayload("handler-input")
			require.NoError(t, err)
			called := false
			operation := nexus.NewSyncOperation(
				"handler-operation",
				func(ctx context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
					called = true
					require.True(t, IsNexusOperation(ctx))
					require.Equal(t, "handler-input", input)
					return "handler-result", nil
				},
			)
			service := nexus.NewService("handler-service")
			require.NoError(t, service.Register(operation))
			registry := nexus.NewServiceRegistry()
			require.NoError(t, registry.Register(service))
			registry.Use(nexusMiddleware(nil))
			handler, err := registry.NewHandler()
			require.NoError(t, err)
			taskHandler := newNexusTaskHandler(
				handler, "identity", "namespace", "task-queue", nil, dc,
				GetDefaultFailureConverter(), ilog.NewNopLogger(), metrics.NopHandler, newRegistry(),
			)
			completed, failed, err := taskHandler.Execute(&workflowservice.PollNexusTaskQueueResponse{
				TaskToken: []byte("task-token"),
				Request: &nexuspb.Request{
					Endpoint: "handler-endpoint",
					Header: map[string]string{
						nexus.HeaderRequestTimeout: "1m",
						"test-header":              "header-value",
					},
					Variant: &nexuspb.Request_StartOperation{
						StartOperation: &nexuspb.StartOperationRequest{
							Service:   "handler-service",
							Operation: "handler-operation",
							RequestId: "request-id",
							Payload:   inputPayload,
						},
					},
				},
			})
			require.NoError(t, err)
			require.Nil(t, failed)
			require.True(t, called)
			var result string
			require.NoError(t, converter.GetDefaultDataConverter().FromPayload(
				completed.GetResponse().GetStartOperation().GetSyncSuccess().GetPayload(), &result,
			))
			require.Equal(t, "handler-result", result)
		})
	}
}
