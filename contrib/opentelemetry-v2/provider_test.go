package opentelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type wrappedTracerProvider struct {
	trace.TracerProvider
}

func setTracerProvider(t *testing.T, provider trace.TracerProvider) {
	t.Helper()
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
	})
}

func TestReplaySafeTracerProviderValidation(t *testing.T) {
	replaySafeProvider := NewReplaySafeTracerProvider()
	t.Cleanup(func() {
		require.NoError(t, replaySafeProvider.Shutdown(context.Background()))
	})

	tests := []struct {
		name     string
		provider trace.TracerProvider
		panic    bool
	}{
		{name: "replay-safe", provider: replaySafeProvider},
		{name: "no-op", provider: noop.NewTracerProvider(), panic: true},
		{name: "otel-sdk", provider: sdktrace.NewTracerProvider(), panic: true},
		{name: "wrapped", provider: &wrappedTracerProvider{TracerProvider: replaySafeProvider}, panic: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setTracerProvider(t, test.provider)

			if test.panic {
				require.Panics(t, func() { NewPlugin(PluginOptions{}) })
				require.Panics(t, func() { Tracer("test") })
				return
			}

			plugin, err := NewPlugin(PluginOptions{})
			require.NoError(t, err)
			require.NotNil(t, plugin)
			require.NotNil(t, Tracer("test"))
		})
	}
}
