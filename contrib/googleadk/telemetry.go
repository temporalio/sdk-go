package googleadk

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// ADK records its telemetry (spans, gen_ai.* log events, and — once upstream
// adk-go TODO(#479) lands — metrics) through the OpenTelemetry process globals,
// from code that runs inside the workflow. Workflow code re-executes on every
// history replay, so without a gate each replay re-emits all of it: token-usage
// numbers are re-read from history and re-attached identically, inflating
// observed counts by one full copy per replay. The log and metric wrappers
// below suppress emissions whose context carries a workflow.Context (stashed
// by NewContext under wfCtxKey, refreshed per fan-out coroutine) that reports
// workflow.IsReplaying outside a read-only context, so recordings happen on
// first execution only — the same semantics as workflow.GetMetricsHandler.
// The tracer wrapper gates span End rather than Start (see
// NewReplaySafeTracerProvider): replays re-create spans without re-exporting
// them, and a span cut off by eviction or restart is exported once, complete,
// by the catch-up replay's live End. Contexts without a workflow.Context
// always pass through, so the wrappers are safe to install process-wide:
// worker, client, and Activity telemetry is untouched.

// replaySuppressed reports whether an emission carrying ctx originates from
// workflow code that is re-executing history: workflow.IsReplaying composed
// with !workflow.IsReadOnly (Experimental). The read-only contexts — query
// handlers, update validators, and side-effect functions — are live or
// non-replayed and never suppressed: queries run once per request and never
// re-execute from history (IsReplaying merely retains whatever the last
// processed history event left in the flag), the SDK skips validators when
// replaying accepted updates, and side-effect functions do not run during
// replay at all (their recorded markers supply the value), so IsReadOnly never
// coincides with an actual history re-execution.
func replaySuppressed(ctx context.Context) bool {
	wfCtx, ok := workflowContext(ctx)
	if !ok {
		return false
	}
	return workflow.IsReplaying(wfCtx) && !workflow.IsReadOnly(wfCtx)
}

// spanRandomStreamPrefix names the per-tracer workflow random streams feeding
// the span-ID generator, so a workflow span re-created on replay draws the same
// trace and span IDs it drew on first execution.
const spanRandomStreamPrefix = "go.temporal.io/sdk/contrib/googleadk/spans/"

// otelRandomKey carries the io.Reader the span-ID generator draws from.
type otelRandomKey struct{}

// workflowSpanIDGenerator draws trace and span IDs from the io.Reader the
// replay-safe tracer attaches to the span-start context (a
// workflow.GetRandomStream for sequenced workflow spans) and falls back to
// crypto/rand for every other span. NewReplaySafeTracerProvider force-installs
// it so a workflow span re-created during replay keeps its first-execution
// identity: a span cut off by eviction, shutdown, or crash is exported after
// the catch-up replay under its original trace and span IDs, and spans exported
// live keep valid parent links into spans whose re-creation was itself never
// re-exported.
type workflowSpanIDGenerator struct{}

func (g workflowSpanIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	var tid trace.TraceID
	for !tid.IsValid() {
		readSpanRandom(ctx, tid[:])
	}
	return tid, g.NewSpanID(ctx, tid)
}

func (workflowSpanIDGenerator) NewSpanID(ctx context.Context, _ trace.TraceID) trace.SpanID {
	var sid trace.SpanID
	for !sid.IsValid() {
		readSpanRandom(ctx, sid[:])
	}
	return sid
}

func readSpanRandom(ctx context.Context, p []byte) {
	r, _ := ctx.Value(otelRandomKey{}).(io.Reader)
	if r == nil {
		r = cryptorand.Reader
	}
	_, _ = io.ReadFull(r, p)
}

// ReplaySafeTracerProvider owns the sdktrace.TracerProvider it is built around
// and force-installs the workflow span-ID generator, so the deterministic-ID
// generator that lets a replay-recreated span keep its identity can never be
// omitted. It embeds the owned *sdktrace.TracerProvider, so Shutdown and
// ForceFlush promote through; the caller owns the provider and must shut it down
// after its clients and workers stop.
type ReplaySafeTracerProvider struct {
	*sdktrace.TracerProvider
}

// NewReplaySafeTracerProvider builds an sdktrace.TracerProvider from opts and
// force-installs the workflow span-ID generator so spans started from sequenced
// workflow code are real spans in every execution mode, replay included, but
// are exported at most once: End is suppressed while the workflow is replaying
// (and during eviction teardown, which the SDK marks as replay), so a span
// whose lifetime lay entirely inside replayed history is re-created but never
// re-exported, while a span cut off by eviction, shutdown, or crash is exported
// by the catch-up replay's live End with everything it accumulated — for ADK's
// generate_content spans, the gen_ai.usage.* token attributes. Spans started
// during replay carry a workflow-time start timestamp; live spans keep
// wall-clock starts. Everything without a workflow context on it delegates to
// the owned provider unchanged.
//
// Any WithIDGenerator in opts is overridden: the generator is what gives a
// re-created span its first-execution trace and span IDs, so the provider always
// owns it.
//
// Install it as the FIRST global tracer provider in the process:
//
//	tp := googleadk.NewReplaySafeTracerProvider(sdktrace.WithBatcher(exporter))
//	otel.SetTracerProvider(tp)
//	defer tp.Shutdown(ctx)
//
// ADK captures otel.GetTracerProvider().Tracer(...) at package init, and the
// OTel global proxy binds its delegate on the first SetTracerProvider call
// only — if some other provider is installed first, ADK's cached tracer
// bypasses this wrapper permanently. See "Telemetry and replay" in the README
// for the span-lifetime contract.
func NewReplaySafeTracerProvider(opts ...sdktrace.TracerProviderOption) *ReplaySafeTracerProvider {
	opts = append([]sdktrace.TracerProviderOption(nil), opts...)
	opts = append(opts, sdktrace.WithIDGenerator(workflowSpanIDGenerator{}))
	return &ReplaySafeTracerProvider{sdktrace.NewTracerProvider(opts...)}
}

func (p *ReplaySafeTracerProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return replaySafeTracer{Tracer: p.TracerProvider.Tracer(name, opts...), name: name, owner: p}
}

type replaySafeTracer struct {
	trace.Tracer
	name  string
	owner *ReplaySafeTracerProvider
}

func (t replaySafeTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	wfCtx, ok := workflowContext(ctx)
	if !ok || workflow.IsReadOnly(wfCtx) {
		// Non-workflow spans and read-only (query/validator/side-effect) spans
		// are once-per-request: ordinary random IDs, wall-clock times, real End.
		return t.Tracer.Start(ctx, spanName, opts...)
	}

	// Sequenced workflow span. Exported spans have two lifecycles: start live
	// and end live (wall-clock), or start during replay (workflow time) and
	// end live. An End reached while still replaying is suppressed, so replays
	// re-create spans without re-exporting them.
	ctx = context.WithValue(ctx, otelRandomKey{}, workflow.GetRandomStream(wfCtx, spanRandomStreamPrefix+t.name))
	if workflow.IsReplaying(wfCtx) {
		// Prepended so a caller-supplied WithTimestamp still wins.
		opts = append([]trace.SpanStartOption{trace.WithTimestamp(workflow.Now(wfCtx))}, opts...)
	}

	spanCtx, span := t.Tracer.Start(ctx, spanName, opts...)
	wrapped := &workflowSpan{Span: span, wfCtx: wfCtx, provider: t.owner}
	return trace.ContextWithSpan(spanCtx, wrapped), wrapped
}

// workflowSpan suppresses End while its workflow is replaying. The SDK also
// flags eviction teardown as replay before running coroutine defers, so ADK's
// deferred force-End of a still-open span exports nothing at eviction; the
// span is exported once, by whichever execution reaches its End live.
type workflowSpan struct {
	trace.Span
	wfCtx    workflow.Context
	provider *ReplaySafeTracerProvider
}

func (s *workflowSpan) End(opts ...trace.SpanEndOption) {
	if workflow.IsReplaying(s.wfCtx) {
		return
	}
	s.Span.End(opts...)
}

// TracerProvider returns the owning replay-safe provider rather than the inner
// span's provider: deriving a tracer from a span is the documented way to create
// more spans on the span's pipeline, and an unwrapped tracer reached that way
// from workflow code would bypass the End gate and the stream-fed span IDs. The
// owned provider carries the generator, so tracers derived through it stay
// replay-safe.
func (s *workflowSpan) TracerProvider() trace.TracerProvider {
	return s.provider
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
// (Add/Record) made from replaying workflow code are dropped and each sync
// instrument's Enabled reports false, matching the wrapped logger's Enabled;
// everything else delegates to inner unchanged. Observable (asynchronous)
// instruments and RegisterCallback pass through untouched: their callbacks
// run on the metric reader's collect cycle, never under a workflow context.
//
// The pinned adk-go emits no OTel metrics yet (meter-provider init is upstream
// TODO(#479)), so today this wrapper matters for workflow-side metrics your own
// code records through the global meter; once ADK metrics arrive they are
// covered too. Install it as the FIRST global meter provider in the process:
//
//	otel.SetMeterProvider(googleadk.NewReplaySafeMeterProvider(realProvider))
//
// The OTel global proxy binds its delegate on the first SetMeterProvider call
// only — if some other provider is installed first, a meter captured through
// the proxy bypasses this wrapper permanently. Workflow-side recordings happen
// on first execution only (the workflow.GetMetricsHandler semantics); replays
// add nothing.
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

func (c replaySafeInt64Counter) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return c.Int64Counter.Enabled(ctx)
}

type replaySafeInt64UpDownCounter struct{ metric.Int64UpDownCounter }

func (c replaySafeInt64UpDownCounter) Add(ctx context.Context, incr int64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Int64UpDownCounter.Add(ctx, incr, opts...)
}

func (c replaySafeInt64UpDownCounter) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return c.Int64UpDownCounter.Enabled(ctx)
}

type replaySafeInt64Histogram struct{ metric.Int64Histogram }

func (h replaySafeInt64Histogram) Record(ctx context.Context, v int64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	h.Int64Histogram.Record(ctx, v, opts...)
}

func (h replaySafeInt64Histogram) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return h.Int64Histogram.Enabled(ctx)
}

type replaySafeInt64Gauge struct{ metric.Int64Gauge }

func (g replaySafeInt64Gauge) Record(ctx context.Context, v int64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	g.Int64Gauge.Record(ctx, v, opts...)
}

func (g replaySafeInt64Gauge) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return g.Int64Gauge.Enabled(ctx)
}

type replaySafeFloat64Counter struct{ metric.Float64Counter }

func (c replaySafeFloat64Counter) Add(ctx context.Context, incr float64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Float64Counter.Add(ctx, incr, opts...)
}

func (c replaySafeFloat64Counter) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return c.Float64Counter.Enabled(ctx)
}

type replaySafeFloat64UpDownCounter struct{ metric.Float64UpDownCounter }

func (c replaySafeFloat64UpDownCounter) Add(ctx context.Context, incr float64, opts ...metric.AddOption) {
	if replaySuppressed(ctx) {
		return
	}
	c.Float64UpDownCounter.Add(ctx, incr, opts...)
}

func (c replaySafeFloat64UpDownCounter) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return c.Float64UpDownCounter.Enabled(ctx)
}

type replaySafeFloat64Histogram struct{ metric.Float64Histogram }

func (h replaySafeFloat64Histogram) Record(ctx context.Context, v float64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	h.Float64Histogram.Record(ctx, v, opts...)
}

func (h replaySafeFloat64Histogram) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return h.Float64Histogram.Enabled(ctx)
}

type replaySafeFloat64Gauge struct{ metric.Float64Gauge }

func (g replaySafeFloat64Gauge) Record(ctx context.Context, v float64, opts ...metric.RecordOption) {
	if replaySuppressed(ctx) {
		return
	}
	g.Float64Gauge.Record(ctx, v, opts...)
}

func (g replaySafeFloat64Gauge) Enabled(ctx context.Context) bool {
	if replaySuppressed(ctx) {
		return false
	}
	return g.Float64Gauge.Enabled(ctx)
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
// recognized. NewPlugin runs this at worker start and workflow replayer
// creation against the OTel process globals.
func warnOnNonReplaySafeTelemetryProviders(logger log.Logger, tracerProvider, loggerProvider, meterProvider any) {
	warn := func(global, wrapper string, p any) {
		if !isRawOTelSDKProvider(p) {
			return
		}
		logger.Warn(fmt.Sprintf(
			"The global OpenTelemetry %s is not replay-safe: ADK emits telemetry through it "+
				"from workflow code, and every history replay will re-emit one full copy. Install a "+
				"replay-safe provider from googleadk.%s as the first global provider set in the "+
				"process; see \"Telemetry and replay\" in the contrib/googleadk README.",
			global, wrapper),
			"provider", fmt.Sprintf("%T", p))
	}
	warn("tracer provider", "NewReplaySafeTracerProvider", tracerProvider)
	warn("logger provider", "NewReplaySafeLoggerProvider", loggerProvider)
	warn("meter provider", "NewReplaySafeMeterProvider", meterProvider)
}
