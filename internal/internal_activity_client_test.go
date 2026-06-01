package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
)

// headerCheckInterceptor is a ClientInterceptor that verifies the header is
// present on the context when ExecuteActivity is called. This ensures that
// contextWithNewHeader is called before the interceptor chain runs, so
// interceptors (like the tracing interceptor) can read/write headers.
type headerCheckInterceptor struct {
	ClientInterceptorBase
	headerWasPresent bool
}

func (h *headerCheckInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	return &headerCheckOutbound{
		ClientOutboundInterceptorBase: ClientOutboundInterceptorBase{Next: next},
		parent:                        h,
	}
}

type headerCheckOutbound struct {
	ClientOutboundInterceptorBase
	parent *headerCheckInterceptor
}

func (h *headerCheckOutbound) ExecuteActivity(
	ctx context.Context,
	in *ClientExecuteActivityInput,
) (ClientActivityHandle, error) {
	h.parent.headerWasPresent = Header(ctx) != nil
	// Return an error to short-circuit the rest of the chain (avoids needing a
	// real gRPC connection for the base interceptor).
	return nil, fmt.Errorf("short-circuit")
}

func TestExecuteActivityHeaderAvailableToInterceptors(t *testing.T) {
	interceptor := &headerCheckInterceptor{}

	client := NewServiceClient(nil, nil, ClientOptions{
		Interceptors: []ClientInterceptor{interceptor},
	})
	// Pre-set capabilities so ensureInitialized doesn't make a gRPC call.
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{}

	// Register a dummy activity so getValidatedActivityFunction succeeds.
	dummyActivity := func(ctx context.Context) error { return nil }
	client.registry.RegisterActivityWithOptions(dummyActivity, RegisterActivityOptions{})

	_, err := client.ExecuteActivity(context.Background(), ClientStartActivityOptions{
		TaskQueue:           "test-tq",
		ID:                  "test-activity-id",
		StartToCloseTimeout: 1,
	}, dummyActivity)
	// We expect the short-circuit error from our interceptor.
	require.ErrorContains(t, err, "short-circuit")
	require.True(t, interceptor.headerWasPresent,
		"Header should be set on context before interceptor chain runs")
}

func TestStartActivityOptions_PausePolicy(t *testing.T) {
	dc := converter.GetDefaultDataConverter()

	t.Run("set", func(t *testing.T) {
		options := ClientStartActivityOptions{
			ID:                  "test-activity-id",
			TaskQueue:           "test-tq",
			StartToCloseTimeout: 1,
			PausePolicy:         PausePolicy{MaxAttempts: 3},
		}
		request := &workflowservice.StartActivityExecutionRequest{}
		require.NoError(t, options.validateAndSetInRequest(request, dc))
		require.NotNil(t, request.PausePolicy)
		require.Equal(t, int32(3), request.PausePolicy.GetMaxAttempts())
	})

	t.Run("zero value sends nil", func(t *testing.T) {
		options := ClientStartActivityOptions{
			ID:                  "test-activity-id",
			TaskQueue:           "test-tq",
			StartToCloseTimeout: 1,
		}
		request := &workflowservice.StartActivityExecutionRequest{}
		require.NoError(t, options.validateAndSetInRequest(request, dc))
		require.Nil(t, request.PausePolicy)
	})
}
