package googleadk

import (
	"context"
	"fmt"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// ADK records its telemetry (spans, gen_ai.* log events, and — once upstream
// adk-go TODO(#479) lands — metrics) through the OpenTelemetry process globals,
// from code that runs inside the workflow. Workflow code re-executes on every
// history replay, so without a gate each replay re-emits all of it: token-usage
// numbers are re-read from history and re-attached identically, inflating
// observed counts by one full copy per replay. The wrappers below suppress
// emissions whose context carries a workflow.Context (stashed by NewContext
// under wfCtxKey, refreshed per fan-out coroutine) that reports
// workflow.IsReplaying. Recordings therefore happen on first execution only —
// the same semantics as workflow.GetMetricsHandler. Contexts without a
// workflow.Context always pass through, so the wrappers are safe to install
// process-wide: worker, client, and Activity telemetry is untouched.

// replaySuppressed reports whether an emission carrying ctx originates from
// workflow code that is currently replaying history.
func replaySuppressed(ctx context.Context) bool {
	wfCtx, ok := workflowContext(ctx)
	if !ok {
		return false
	}
	return workflow.IsReplaying(wfCtx)
}

// suppressedTracer produces the non-recording spans returned while replaying.
var suppressedTracer = noop.NewTracerProvider().Tracer("googleadk.replay-suppressed")

// NewReplaySafeTracerProvider wraps inner so spans started from replaying
// workflow code become non-recording no-ops; everything else delegates to
// inner unchanged.
//
// Install it as the FIRST global tracer provider in the process:
//
//	otel.SetTracerProvider(googleadk.NewReplaySafeTracerProvider(realProvider))
//
// ADK captures otel.GetTracerProvider().Tracer(...) at package init, and the
// OTel global proxy binds its delegate on the first SetTracerProvider call
// only — if some other provider is installed first, ADK's cached tracer
// bypasses this wrapper permanently. Replays add no spans, but a span still
// open when its workflow leaves the worker is truncated (sticky-cache
// eviction) or lost (worker shutdown, crash): the replay re-creation on
// resume is non-recording. See "Telemetry and replay" in the README for the
// span-lifetime contract.
func NewReplaySafeTracerProvider(inner trace.TracerProvider) trace.TracerProvider {
	return replaySafeTracerProvider{TracerProvider: inner}
}

type replaySafeTracerProvider struct{ trace.TracerProvider }

func (p replaySafeTracerProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return replaySafeTracer{Tracer: p.TracerProvider.Tracer(name, opts...)}
}

type replaySafeTracer struct{ trace.Tracer }

func (t replaySafeTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if replaySuppressed(ctx) {
		return suppressedTracer.Start(ctx, spanName, opts...)
	}
	return t.Tracer.Start(ctx, spanName, opts...)
}

// NewReplaySafeLoggerProvider wraps inner so log records emitted from replaying
// workflow code are dropped; everything else delegates to inner unchanged.
//
// Install it as the FIRST global logger provider in the process:
//
//	global.SetLoggerProvider(googleadk.NewReplaySafeLoggerProvider(realProvider))
//
// ADK captures the global logger at package init, and the OTel global proxy
// binds its delegate on the first SetLoggerProvider call only — if some other
// provider is installed first, ADK's cached logger bypasses this wrapper
// permanently. Workflow-side records (ADK's gen_ai.* events) are emitted on
// first execution only; replays add nothing.
func NewReplaySafeLoggerProvider(inner otellog.LoggerProvider) otellog.LoggerProvider {
	return replaySafeLoggerProvider{LoggerProvider: inner}
}

type replaySafeLoggerProvider struct{ otellog.LoggerProvider }

func (p replaySafeLoggerProvider) Logger(name string, opts ...otellog.LoggerOption) otellog.Logger {
	return replaySafeLogger{Logger: p.LoggerProvider.Logger(name, opts...)}
}

type replaySafeLogger struct{ otellog.Logger }

func (l replaySafeLogger) Emit(ctx context.Context, r otellog.Record) {
	if replaySuppressed(ctx) {
		return
	}
	l.Logger.Emit(ctx, r)
}

func (l replaySafeLogger) Enabled(ctx context.Context, param otellog.EnabledParameters) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return l.Logger.Enabled(ctx, param)
}

// NewReplaySafeMeterProvider wraps inner so synchronous instrument recordings
// (Add/Record) made from replaying workflow code are dropped; everything else
// delegates to inner unchanged. Observable (asynchronous) instruments and
// RegisterCallback pass through untouched: their callbacks run on the metric
// reader's collect cycle, never under a workflow context.
//
// The pinned adk-go emits no OTel metrics yet (meter-provider init is upstream
// TODO(#479)), so today this wrapper matters for workflow-side metrics your own
// code records through the global meter; once ADK metrics arrive they are
// covered too. Install it as the FIRST global meter provider in the process,
// before any package init captures the global proxy:
//
//	otel.SetMeterProvider(googleadk.NewReplaySafeMeterProvider(realProvider))
//
// Workflow-side recordings happen on first execution only (the
// workflow.GetMetricsHandler semantics); replays add nothing.
func NewReplaySafeMeterProvider(inner metric.MeterProvider) metric.MeterProvider {
	return replaySafeMeterProvider{MeterProvider: inner}
}

type replaySafeMeterProvider struct{ metric.MeterProvider }

func (p replaySafeMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return replaySafeMeter{Meter: p.MeterProvider.Meter(name, opts...)}
}

// replaySafeMeter overrides only the synchronous instrument constructors. The
// observable constructors and RegisterCallback come from the embedded Meter,
// so callback registration sees the inner meter's own instruments.
//
// The OTel API contract requires meters to return a usable instrument even
// alongside a non-nil error (the SDK does so for e.g. invalid instrument
// names), so each constructor wraps whatever non-nil instrument the inner
// meter returned and propagates the error as-is.
type replaySafeMeter struct{ metric.Meter }

func (m replaySafeMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	inner, err := m.Meter.Int64Counter(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeInt64Counter{Int64Counter: inner}, err
}

func (m replaySafeMeter) Int64UpDownCounter(name string, opts ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	inner, err := m.Meter.Int64UpDownCounter(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeInt64UpDownCounter{Int64UpDownCounter: inner}, err
}

func (m replaySafeMeter) Int64Histogram(name string, opts ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	inner, err := m.Meter.Int64Histogram(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeInt64Histogram{Int64Histogram: inner}, err
}

func (m replaySafeMeter) Int64Gauge(name string, opts ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	inner, err := m.Meter.Int64Gauge(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeInt64Gauge{Int64Gauge: inner}, err
}

func (m replaySafeMeter) Float64Counter(name string, opts ...metric.Float64CounterOption) (metric.Float64Counter, error) {
	inner, err := m.Meter.Float64Counter(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeFloat64Counter{Float64Counter: inner}, err
}

func (m replaySafeMeter) Float64UpDownCounter(name string, opts ...metric.Float64UpDownCounterOption) (metric.Float64UpDownCounter, error) {
	inner, err := m.Meter.Float64UpDownCounter(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeFloat64UpDownCounter{Float64UpDownCounter: inner}, err
}

func (m replaySafeMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	inner, err := m.Meter.Float64Histogram(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeFloat64Histogram{Float64Histogram: inner}, err
}

func (m replaySafeMeter) Float64Gauge(name string, opts ...metric.Float64GaugeOption) (metric.Float64Gauge, error) {
	inner, err := m.Meter.Float64Gauge(name, opts...)
	if inner == nil {
		return nil, err
	}
	return replaySafeFloat64Gauge{Float64Gauge: inner}, err
}

type replaySafeInt64Counter struct{ metric.Int64Counter }

func (c replaySafeInt64Counter) Add(ctx context.Context, incr int64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Int64Counter.Add(ctx, incr, opts...)
}

type replaySafeInt64UpDownCounter struct{ metric.Int64UpDownCounter }

func (c replaySafeInt64UpDownCounter) Add(ctx context.Context, incr int64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Int64UpDownCounter.Add(ctx, incr, opts...)
}

type replaySafeInt64Histogram struct{ metric.Int64Histogram }

func (h replaySafeInt64Histogram) Record(ctx context.Context, v int64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	h.Int64Histogram.Record(ctx, v, opts...)
}

type replaySafeInt64Gauge struct{ metric.Int64Gauge }

func (g replaySafeInt64Gauge) Record(ctx context.Context, v int64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	g.Int64Gauge.Record(ctx, v, opts...)
}

type replaySafeFloat64Counter struct{ metric.Float64Counter }

func (c replaySafeFloat64Counter) Add(ctx context.Context, incr float64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Float64Counter.Add(ctx, incr, opts...)
}

type replaySafeFloat64UpDownCounter struct{ metric.Float64UpDownCounter }

func (c replaySafeFloat64UpDownCounter) Add(ctx context.Context, incr float64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Float64UpDownCounter.Add(ctx, incr, opts...)
}

type replaySafeFloat64Histogram struct{ metric.Float64Histogram }

func (h replaySafeFloat64Histogram) Record(ctx context.Context, v float64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	h.Float64Histogram.Record(ctx, v, opts...)
}

type replaySafeFloat64Gauge struct{ metric.Float64Gauge }

func (g replaySafeFloat64Gauge) Record(ctx context.Context, v float64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	g.Float64Gauge.Record(ctx, v, opts...)
}

// isRawOTelSDKProvider reports whether p is one of the concrete OTel SDK
// provider types installed unwrapped — the only providers positively known to
// record everything replaying workflow code re-emits.
func isRawOTelSDKProvider(p any) bool {
	switch p.(type) {
	case *sdktrace.TracerProvider, *sdklog.LoggerProvider, *sdkmetric.MeterProvider:
		return true
	}
	return false
}

// warnOnNonReplaySafeTelemetryProviders logs one warning per global provider
// that is a raw OTel SDK provider; everything it cannot positively classify —
// the replay-safe wrappers, noop providers, the never-set global proxies, and
// custom or wrapping providers — stays silent rather than risking a false
// warning. Best-effort by construction: the OTel global proxy binds its
// delegate on the FIRST Set*Provider call permanently, so in a process that
// sets a global more than once the provider visible here is not necessarily
// the one ADK's package-init capture went through. A raw SDK provider
// installed once and never wrapped, the realistic misconfiguration, is
// recognized. NewPlugin runs this at worker start against the OTel process
// globals.
func warnOnNonReplaySafeTelemetryProviders(logger log.Logger, tracerProvider, loggerProvider, meterProvider any) {
	warn := func(global, wrapper string, p any) {
		if !isRawOTelSDKProvider(p) {
			return
		}
		logger.Warn("The global OpenTelemetry "+global+" is not replay-safe: ADK emits telemetry through it from workflow code, and every history replay will re-emit one full copy. Wrap it with googleadk."+wrapper+" and install the wrapper as the first global provider set in the process; see \"Telemetry and replay\" in the contrib/googleadk README.",
			"provider", fmt.Sprintf("%T", p))
	}
	warn("tracer provider", "NewReplaySafeTracerProvider", tracerProvider)
	warn("logger provider", "NewReplaySafeLoggerProvider", loggerProvider)
	warn("meter provider", "NewReplaySafeMeterProvider", meterProvider)
}
