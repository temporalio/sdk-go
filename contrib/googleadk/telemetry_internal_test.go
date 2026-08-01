package googleadk

// Unit tests for the best-effort provider classifier behind the worker-start
// warning. The unbound global proxies are captured at binary init: the OTel
// proxy binds its delegate on the first Set*Provider call permanently, and
// telemetry_test.go installs real globals during the run, so reading the
// globals from inside a test would depend on test order. Nothing in this file
// may call otel.Set*Provider — that would bind the one-shot proxy delegates
// and break the external replay-telemetry tests.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	otelloglobal "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

var (
	unboundProxyTracerProvider = otel.GetTracerProvider()
	unboundProxyLoggerProvider = otelloglobal.GetLoggerProvider()
	unboundProxyMeterProvider  = otel.GetMeterProvider()
)

func TestClassifyTelemetryProvider(t *testing.T) {
	cases := []struct {
		name     string
		provider any
		want     telemetryProviderSafety
	}{
		{"replay-safe tracer wrapper", NewReplaySafeTracerProvider(tracenoop.NewTracerProvider()), telemetryProviderReplaySafe},
		{"replay-safe logger wrapper", NewReplaySafeLoggerProvider(lognoop.NewLoggerProvider()), telemetryProviderReplaySafe},
		{"replay-safe meter wrapper", NewReplaySafeMeterProvider(metricnoop.NewMeterProvider()), telemetryProviderReplaySafe},
		{"noop tracer provider", tracenoop.NewTracerProvider(), telemetryProviderReplaySafe},
		{"noop logger provider", lognoop.NewLoggerProvider(), telemetryProviderReplaySafe},
		{"noop meter provider", metricnoop.NewMeterProvider(), telemetryProviderReplaySafe},
		{"pointer to noop tracer provider", &tracenoop.TracerProvider{}, telemetryProviderReplaySafe},
		//lint:ignore SA1019 users may still install the deprecated noop provider; it must not draw a warning
		{"deprecated otel noop tracer provider", trace.NewNoopTracerProvider(), telemetryProviderReplaySafe},
		{"unbound global proxy tracer provider", unboundProxyTracerProvider, telemetryProviderUnknown},
		{"unbound global proxy logger provider", unboundProxyLoggerProvider, telemetryProviderUnknown},
		{"unbound global proxy meter provider", unboundProxyMeterProvider, telemetryProviderUnknown},
		{"nil", nil, telemetryProviderUnknown},
		{"raw SDK tracer provider", sdktrace.NewTracerProvider(), telemetryProviderNotReplaySafe},
		{"raw SDK logger provider", sdklog.NewLoggerProvider(), telemetryProviderNotReplaySafe},
		{"raw SDK meter provider", sdkmetric.NewMeterProvider(), telemetryProviderNotReplaySafe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyTelemetryProvider(tc.provider))
		})
	}
}

type warnCapturingLogger struct {
	warnings []string
}

func (l *warnCapturingLogger) Debug(string, ...interface{}) {}
func (l *warnCapturingLogger) Info(string, ...interface{})  {}
func (l *warnCapturingLogger) Error(string, ...interface{}) {}
func (l *warnCapturingLogger) Warn(msg string, keyvals ...interface{}) {
	l.warnings = append(l.warnings, fmt.Sprint(append([]interface{}{msg}, keyvals...)...))
}

func TestWarnOnNonReplaySafeTelemetryProviders(t *testing.T) {
	t.Run("RawProvidersWarnNamingTheWrapper", func(t *testing.T) {
		logger := &warnCapturingLogger{}
		warnOnNonReplaySafeTelemetryProviders(logger,
			sdktrace.NewTracerProvider(), sdklog.NewLoggerProvider(), sdkmetric.NewMeterProvider())
		require.Len(t, logger.warnings, 3)
		for i, wrapper := range []string{"NewReplaySafeTracerProvider", "NewReplaySafeLoggerProvider", "NewReplaySafeMeterProvider"} {
			assert.Contains(t, logger.warnings[i], wrapper, "each warning must name the wrapper constructor to install")
		}
	})

	t.Run("WrappedAndUnsetProvidersStaySilent", func(t *testing.T) {
		logger := &warnCapturingLogger{}
		warnOnNonReplaySafeTelemetryProviders(logger,
			NewReplaySafeTracerProvider(sdktrace.NewTracerProvider()),
			unboundProxyLoggerProvider,
			NewReplaySafeMeterProvider(sdkmetric.NewMeterProvider()))
		require.Empty(t, logger.warnings)
	})

	t.Run("OnlyTheUnsafeProviderWarns", func(t *testing.T) {
		logger := &warnCapturingLogger{}
		warnOnNonReplaySafeTelemetryProviders(logger,
			NewReplaySafeTracerProvider(sdktrace.NewTracerProvider()),
			sdklog.NewLoggerProvider(),
			unboundProxyMeterProvider)
		require.Len(t, logger.warnings, 1)
		assert.Contains(t, logger.warnings[0], "NewReplaySafeLoggerProvider")
	})
}
