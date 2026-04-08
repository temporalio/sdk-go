package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
)

// nexusHeaderCheckInterceptor is a ClientInterceptor that verifies the header is
// present on the context when ExecuteNexusOperation is called. This ensures that
// contextWithNewHeader is called before the interceptor chain runs, so
// interceptors (like the tracing interceptor) can read/write headers.
type nexusHeaderCheckInterceptor struct {
	ClientInterceptorBase
	headerWasPresent bool
}

func (h *nexusHeaderCheckInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &nexusHeaderCheckOutbound{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		parent:                        h,
	}
}

type nexusHeaderCheckOutbound struct {
	ClientOutboundInterceptorBase
	parent *nexusHeaderCheckInterceptor
}

func (h *nexusHeaderCheckOutbound) ExecuteNexusOperation(
	ctx context.Context,
	in *ClientExecuteNexusOperationInput,
) (ClientNexusOperationHandle, error) {
	h.parent.headerWasPresent = Header(ctx) != nil
	// Return an error to short-circuit the rest of the chain (avoids needing a
	// real gRPC connection for the base interceptor).
	return nil, fmt.Errorf("short-circuit")
}

func TestExecuteNexusOperationHeaderAvailableToInterceptors(t *testing.T) {
	interceptor := &nexusHeaderCheckInterceptor{}

	client := NewServiceClient(nil, nil, ClientOptions{
		Interceptors: []ClientInterceptor{interceptor},
	})
	// Pre-set capabilities so ensureInitialized doesn't make a gRPC call.
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	nexusClient, err := client.NewNexusClient(ClientNexusClientOptions{
		Endpoint: "test-endpoint",
		Service:  "test-service",
	})
	require.NoError(t, err)

	_, err = nexusClient.ExecuteOperation(context.Background(), "test-op", "test-input", ClientStartNexusOperationOptions{
		ID: "test-op-id",
	})
	// We expect the short-circuit error from our interceptor.
	require.ErrorContains(t, err, "short-circuit")
	require.True(t, interceptor.headerWasPresent,
		"Header should be set on context before interceptor chain runs")
}

func TestNexusClientValidation(t *testing.T) {
	client := NewServiceClient(nil, nil, ClientOptions{})

	_, err := client.NewNexusClient(ClientNexusClientOptions{})
	require.ErrorContains(t, err, "endpoint is required")

	_, err = client.NewNexusClient(ClientNexusClientOptions{Endpoint: "ep"})
	require.ErrorContains(t, err, "service is required")

	nc, err := client.NewNexusClient(ClientNexusClientOptions{Endpoint: "ep", Service: "svc"})
	require.NoError(t, err)
	require.NotNil(t, nc)
}

func TestResolveNexusOperationName(t *testing.T) {
	// String name
	name, err := resolveNexusOperationName("my-op", nil)
	require.NoError(t, err)
	require.Equal(t, "my-op", name)

	// Invalid type
	_, err = resolveNexusOperationName(123, nil)
	require.ErrorContains(t, err, "invalid 'operation' parameter")
}
