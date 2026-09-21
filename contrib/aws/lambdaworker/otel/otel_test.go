package otel

import (
	"context"
	"testing"

	otelsdkmetric "go.opentelemetry.io/otel/sdk/metric"
	otelsdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.temporal.io/sdk/client"
)

// clearOtelEnv makes the OTel-related environment variables deterministic for a
// test by explicitly unsetting them.
func clearOtelEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
}

func TestResolveServiceName(t *testing.T) {
	t.Run("explicit option wins over environment", func(t *testing.T) {
		clearOtelEnv(t)
		t.Setenv("OTEL_SERVICE_NAME", "from-env")
		t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "from-lambda")
		if got := resolveServiceName(Options{ServiceName: "explicit"}); got != "explicit" {
			t.Fatalf("got %q, want %q", got, "explicit")
		}
	})
	t.Run("OTEL_SERVICE_NAME preferred over AWS_LAMBDA_FUNCTION_NAME", func(t *testing.T) {
		clearOtelEnv(t)
		t.Setenv("OTEL_SERVICE_NAME", "from-env")
		t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "from-lambda")
		if got := resolveServiceName(Options{}); got != "from-env" {
			t.Fatalf("got %q, want %q", got, "from-env")
		}
	})
	t.Run("AWS_LAMBDA_FUNCTION_NAME used when OTEL_SERVICE_NAME unset", func(t *testing.T) {
		clearOtelEnv(t)
		t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "from-lambda")
		if got := resolveServiceName(Options{}); got != "from-lambda" {
			t.Fatalf("got %q, want %q", got, "from-lambda")
		}
	})
	t.Run("default when nothing set", func(t *testing.T) {
		clearOtelEnv(t)
		if got := resolveServiceName(Options{}); got != "temporal-lambda-worker" {
			t.Fatalf("got %q, want %q", got, "temporal-lambda-worker")
		}
	})
}

func TestResolveEndpoint(t *testing.T) {
	t.Run("explicit option wins over environment", func(t *testing.T) {
		clearOtelEnv(t)
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "env:4317")
		if got := resolveEndpoint(Options{CollectorEndpoint: "explicit:4317"}); got != "explicit:4317" {
			t.Fatalf("got %q, want %q", got, "explicit:4317")
		}
	})
	t.Run("OTEL_EXPORTER_OTLP_ENDPOINT used when option unset", func(t *testing.T) {
		clearOtelEnv(t)
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "env:4317")
		if got := resolveEndpoint(Options{}); got != "env:4317" {
			t.Fatalf("got %q, want %q", got, "env:4317")
		}
	})
	t.Run("empty when nothing set so exporter default applies", func(t *testing.T) {
		clearOtelEnv(t)
		if got := resolveEndpoint(Options{}); got != "" {
			t.Fatalf("got %q, want empty (exporter default)", got)
		}
	})
}

func TestApplyMetricsSetsHandler(t *testing.T) {
	mp := otelsdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	var opts client.Options
	ApplyMetrics(&opts, mp)

	if opts.MetricsHandler == nil {
		t.Fatal("ApplyMetrics did not set MetricsHandler")
	}
}

func TestApplyTracingAppendsInterceptor(t *testing.T) {
	tp := otelsdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var opts client.Options
	before := len(opts.Interceptors)
	if err := ApplyTracing(&opts, tp); err != nil {
		t.Fatalf("ApplyTracing returned error: %v", err)
	}
	if got := len(opts.Interceptors) - before; got != 1 {
		t.Fatalf("ApplyTracing added %d interceptors, want 1", got)
	}
}

type fakeShutdownRegistrar struct {
	hooks []func(context.Context) error
}

func (f *fakeShutdownRegistrar) OnShutdown(hook func(context.Context) error) {
	f.hooks = append(f.hooks, hook)
}

// TestApplyDefaultsWithProvidersWiring verifies that the AWS Lambda helper wires
// the metrics handler and tracing interceptor onto the client options and
// registers exactly one per-invocation flush hook, using caller-owned in-memory
// providers so no real OTLP exporter is created.
func TestApplyDefaultsWithProvidersWiring(t *testing.T) {
	mp := otelsdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	tp := otelsdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	reg := &fakeShutdownRegistrar{}
	var opts client.Options
	if err := ApplyDefaultsWithProviders(reg, &opts, mp, tp); err != nil {
		t.Fatalf("ApplyDefaultsWithProviders returned error: %v", err)
	}

	if opts.MetricsHandler == nil {
		t.Error("ApplyDefaultsWithProviders did not set MetricsHandler")
	}
	if len(opts.Interceptors) != 1 {
		t.Errorf("ApplyDefaultsWithProviders set %d interceptors, want 1", len(opts.Interceptors))
	}
	if len(reg.hooks) != 1 {
		t.Fatalf("ApplyDefaultsWithProviders registered %d shutdown hooks, want 1", len(reg.hooks))
	}
	// The registered hook force-flushes both providers; on in-memory providers it
	// is a no-op and must not error.
	if err := reg.hooks[0](context.Background()); err != nil {
		t.Errorf("flush hook returned error: %v", err)
	}
}
