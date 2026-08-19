package otlpworker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

func resourceValue(t *testing.T, res *resource.Resource, key attribute.Key) (attribute.Value, bool) {
	t.Helper()
	for _, kv := range res.Attributes() {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestBuildResourceIncludesServiceName(t *testing.T) {
	res, err := buildResource(context.Background(), Config{ServiceName: "my-service"})
	require.NoError(t, err)
	value, ok := resourceValue(t, res, semconv.ServiceNameKey)
	require.True(t, ok)
	require.Equal(t, "my-service", value.AsString())
}

func TestBuildResourceAppliesResourceOptions(t *testing.T) {
	res, err := buildResource(context.Background(), Config{
		ServiceName: "my-service",
		ResourceOptions: []resource.Option{
			resource.WithAttributes(attribute.String("deployment.environment", "test")),
		},
	})
	require.NoError(t, err)
	value, ok := resourceValue(t, res, attribute.Key("deployment.environment"))
	require.True(t, ok)
	require.Equal(t, "test", value.AsString())
	// service.name from the base resource is preserved.
	svc, ok := resourceValue(t, res, semconv.ServiceNameKey)
	require.True(t, ok)
	require.Equal(t, "my-service", svc.AsString())
}

// TestNewProvidersConstructs verifies providers can be built without a running
// collector. The OTLP gRPC exporters connect lazily, so no network is required at
// construction time. Flushing/shutting down would require a live collector and is
// exercised by the higher-level packages' end-to-end harnesses instead.
func TestNewProvidersConstructs(t *testing.T) {
	ctx := context.Background()
	mp, tp, err := NewProviders(ctx, Config{
		ServiceName:          "my-service",
		Endpoint:             "localhost:4317",
		EndpointMode:         EndpointHostPort,
		Insecure:             true,
		MetricExportInterval: 30 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.NotNil(t, tp)
	// Both providers must satisfy the flush/shutdown interfaces used by ForceFlush
	// and Shutdown.
	require.Implements(t, (*interface{ Shutdown(context.Context) error })(nil), mp)
	require.Implements(t, (*interface{ ForceFlush(context.Context) error })(nil), tp)
}

func TestNewProvidersEndpointURLMode(t *testing.T) {
	ctx := context.Background()
	mp, tp, err := NewProviders(ctx, Config{
		ServiceName:  "my-service",
		Endpoint:     "http://localhost:4317",
		EndpointMode: EndpointURL,
	})
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.NotNil(t, tp)
}
