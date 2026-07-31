package googleadk_test

// Regression coverage for replay-safe telemetry. ADK records spans and gen_ai.*
// log events through the OTel process globals from code that runs inside the
// workflow, so without the replay-safe wrappers every history replay re-emits
// all of it (1 real run + N replays => (N+1)x observations) while the real work
// (model Activity invocations) happens exactly once. TestReplayTelemetry proves
// both directions on a real dev server: with the wrappers installed replays add
// zero observations and first-execution telemetry is untouched; without them
// the same replay loop inflates every signal. The remaining tests cover the
// gate's edge cases without a server.

import (
	"context"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	otellog "go.opentelemetry.io/otel/log"
	logembedded "go.opentelemetry.io/otel/log/embedded"
	otelloglobal "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	metricembedded "go.opentelemetry.io/otel/metric/embedded"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	traceembedded "go.opentelemetry.io/otel/trace/embedded"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"go.temporal.io/sdk/contrib/googleadk"
)

const telemetryReplays = 5

// ----------------------------------------------------------------------------
// Switchable global providers. ADK caches the global proxy tracer/logger at
// package init and the OTel global proxy binds its delegate on the FIRST
// Set*Provider call only, so the gated and ungated subtests cannot each own the
// raw process global. Instead a switchable root is installed as the global
// exactly once, and each subtest re-points its target. Resolution happens per
// emission (Start/Emit) because the proxy caches the tracer/logger it got from
// the first delegate.
// ----------------------------------------------------------------------------

type switchableTracerProvider struct {
	traceembedded.TracerProvider
	mu     sync.RWMutex
	target trace.TracerProvider
}

func (p *switchableTracerProvider) set(tp trace.TracerProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = tp
}

func (p *switchableTracerProvider) current() trace.TracerProvider {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

func (p *switchableTracerProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return &switchableTracer{p: p, name: name, opts: opts}
}

type switchableTracer struct {
	traceembedded.Tracer
	p    *switchableTracerProvider
	name string
	opts []trace.TracerOption
}

func (t *switchableTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return t.p.current().Tracer(t.name, t.opts...).Start(ctx, spanName, opts...)
}

type switchableLoggerProvider struct {
	logembedded.LoggerProvider
	mu     sync.RWMutex
	target otellog.LoggerProvider
}

func (p *switchableLoggerProvider) set(lp otellog.LoggerProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = lp
}

func (p *switchableLoggerProvider) current() otellog.LoggerProvider {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

func (p *switchableLoggerProvider) Logger(name string, opts ...otellog.LoggerOption) otellog.Logger {
	return &switchableLogger{p: p, name: name, opts: opts}
}

type switchableLogger struct {
	logembedded.Logger
	p    *switchableLoggerProvider
	name string
	opts []otellog.LoggerOption
}

func (l *switchableLogger) Emit(ctx context.Context, r otellog.Record) {
	l.p.current().Logger(l.name, l.opts...).Emit(ctx, r)
}

func (l *switchableLogger) Enabled(ctx context.Context, param otellog.EnabledParameters) bool {
	return l.p.current().Logger(l.name, l.opts...).Enabled(ctx, param)
}

type switchableMeterProvider struct {
	metricembedded.MeterProvider
	mu     sync.RWMutex
	target metric.MeterProvider
}

func (p *switchableMeterProvider) set(mp metric.MeterProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = mp
}

// Meter resolves the current target immediately: the workflow under test calls
// otel.Meter(...) fresh on every execution and replay, so no per-instrument
// indirection is needed.
func (p *switchableMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target.Meter(name, opts...)
}

var (
	installGlobalsOnce sync.Once
	noopTracerProvider = tracenoop.NewTracerProvider()
	noopLoggerProvider = lognoop.NewLoggerProvider()
	noopMeterProvider  = metricnoop.NewMeterProvider()
	globalSwitchTracer = &switchableTracerProvider{target: noopTracerProvider}
	globalSwitchLogger = &switchableLoggerProvider{target: noopLoggerProvider}
	globalSwitchMeter  = &switchableMeterProvider{target: noopMeterProvider}
)

// pointGlobalsAt installs the switchable roots as the process-global providers
// (once) and points them at the given targets for the duration of the test.
func pointGlobalsAt(t *testing.T, tp trace.TracerProvider, lp otellog.LoggerProvider, mp metric.MeterProvider) {
	t.Helper()
	installGlobalsOnce.Do(func() {
		otel.SetTracerProvider(globalSwitchTracer)
		otelloglobal.SetLoggerProvider(globalSwitchLogger)
		otel.SetMeterProvider(globalSwitchMeter)
	})
	globalSwitchTracer.set(tp)
	globalSwitchLogger.set(lp)
	globalSwitchMeter.set(mp)
	t.Cleanup(func() {
		globalSwitchTracer.set(noopTracerProvider)
		globalSwitchLogger.set(noopLoggerProvider)
		globalSwitchMeter.set(noopMeterProvider)
	})
}

// ----------------------------------------------------------------------------
// In-memory telemetry capture and snapshotting.
// ----------------------------------------------------------------------------

type countingLogProcessor struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCountingLogProcessor() *countingLogProcessor {
	return &countingLogProcessor{counts: map[string]int{}}
}

func (p *countingLogProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

func (p *countingLogProcessor) OnEmit(_ context.Context, r *sdklog.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[r.EventName()]++
	return nil
}

func (p *countingLogProcessor) Shutdown(context.Context) error   { return nil }
func (p *countingLogProcessor) ForceFlush(context.Context) error { return nil }

func (p *countingLogProcessor) snapshot() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.counts))
	for k, v := range p.counts {
		out[k] = v
	}
	return out
}

type telemetryCapture struct {
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
	logs   *countingLogProcessor
}

func newTelemetryCapture() telemetryCapture {
	return telemetryCapture{
		spans:  tracetest.NewSpanRecorder(),
		reader: sdkmetric.NewManualReader(),
		logs:   newCountingLogProcessor(),
	}
}

func (c telemetryCapture) providers() (trace.TracerProvider, otellog.LoggerProvider, metric.MeterProvider) {
	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(c.spans)),
		sdklog.NewLoggerProvider(sdklog.WithProcessor(c.logs)),
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(c.reader))
}

type telemetrySnapshot struct {
	spansByName     map[string]int
	tokenUsageSpans int
	inputTokens     int64
	outputTokens    int64
	logEvents       map[string]int
	counterValue    int64
}

func (c telemetryCapture) snapshot(t *testing.T) telemetrySnapshot {
	t.Helper()
	snap := telemetrySnapshot{spansByName: map[string]int{}, logEvents: c.logs.snapshot()}

	for _, s := range c.spans.Ended() {
		snap.spansByName[s.Name()]++
		for _, attr := range s.Attributes() {
			switch string(attr.Key) {
			case "gen_ai.usage.input_tokens":
				snap.tokenUsageSpans++
				snap.inputTokens += attr.Value.AsInt64()
			case "gen_ai.usage.output_tokens":
				snap.outputTokens += attr.Value.AsInt64()
			}
		}
	}

	snap.counterValue = collectInt64Sum(t, c.reader, telemetryCounterName)
	return snap
}

// collectInt64Sum collects from reader and returns the summed int64 data
// points of the named instrument (0 when absent).
func collectInt64Sum(t *testing.T, reader *sdkmetric.ManualReader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// ----------------------------------------------------------------------------
// Workflow under test: one ADK agent with one in-workflow function tool driven
// by runner.Run (the adapter's documented usage), plus a workflow-side global
// meter recording standing in for the metrics adk-go will emit once upstream
// TODO(#479) lands.
// ----------------------------------------------------------------------------

const telemetryCounterName = "workflow_side_tokens"

type telemetryRunInput struct {
	ModelName   string
	UserMessage string
}

type telemetryRunResult struct {
	Texts []string
}

func telemetryAgentWorkflow(ctx workflow.Context, in telemetryRunInput) (telemetryRunResult, error) {
	weatherTool, err := functiontool.New[map[string]any, map[string]any](
		functiontool.Config{Name: "get_weather", Description: "returns the weather"},
		func(agent.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"weather": "sunny"}, nil
		},
	)
	if err != nil {
		return telemetryRunResult{}, err
	}

	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "telemetry assistant",
		Model:       googleadk.NewModel(in.ModelName),
		Instruction: "be helpful",
		Tools:       []tool.Tool{weatherTool},
	})
	if err != nil {
		return telemetryRunResult{}, err
	}

	r, err := runner.New(runner.Config{
		AppName:           "telemetry-app",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return telemetryRunResult{}, err
	}

	adkCtx := googleadk.NewContext(ctx)

	counter, err := otel.Meter("googleadk-telemetry-test").Int64Counter(telemetryCounterName)
	if err != nil {
		return telemetryRunResult{}, err
	}
	counter.Add(adkCtx, 100)

	msg := genai.NewContentFromText(in.UserMessage, genai.RoleUser)

	var res telemetryRunResult
	for ev, rerr := range r.Run(adkCtx, "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return res, rerr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.Text != "" {
				res.Texts = append(res.Texts, p.Text)
			}
		}
	}
	return res, nil
}

// countingModel wraps FakeModel to count REAL model invocations, which happen
// only inside the InvokeModel Activity. This is the inverse control: replays
// resolve the Activity from history without invoking the model.
type countingModel struct {
	*googleadk.FakeModel
	calls atomic.Int64
}

func (m *countingModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	m.calls.Add(1)
	return m.FakeModel.GenerateContent(ctx, req, stream)
}

// runTelemetryScenario executes telemetryAgentWorkflow once for real on the dev
// server: two scripted model turns carrying token-usage metadata (tool call,
// then final text), one in-workflow tool execution.
func runTelemetryScenario(t *testing.T, c client.Client, taskQueue string) (cm *countingModel, wfID, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	turn1 := googleadk.FunctionCallResponse("call-1", "get_weather", map[string]any{"location": "SF"})
	turn1.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 25, TotalTokenCount: 125,
	}
	turn2 := googleadk.TextResponse("It is sunny.")
	turn2.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 150, CandidatesTokenCount: 12, TotalTokenCount: 162,
	}
	cm = &countingModel{FakeModel: googleadk.NewFakeModel(turn1, turn2)}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(telemetryAgentWorkflow)
	acts, err := googleadk.NewActivities(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": func(context.Context, string) (model.LLM, error) { return cm, nil },
		},
	})
	require.NoError(t, err)
	acts.Register(w)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        taskQueue + "-" + time.Now().Format("150405.000"),
		TaskQueue: taskQueue,
	}, telemetryAgentWorkflow, telemetryRunInput{ModelName: "fake-model", UserMessage: "weather in SF?"})
	require.NoError(t, err)
	var res telemetryRunResult
	require.NoError(t, run.Get(ctx, &res))
	require.Contains(t, strings.Join(res.Texts, " "), "It is sunny.")
	return cm, run.GetID(), run.GetRunID()
}

func replayTelemetryHistory(t *testing.T, c client.Client, wfID, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(telemetryAgentWorkflow)
	for i := 0; i < telemetryReplays; i++ {
		require.NoError(t, replayer.ReplayWorkflowExecution(ctx, c.WorkflowService(), nil, "default", workflow.Execution{
			ID:    wfID,
			RunID: runID,
		}), "replay %d must succeed", i+1)
	}
}

// TestReplayTelemetry is the end-to-end regression test for replay-safe
// telemetry. The subtests share one dev server; each runs one real execution
// and then replays its recorded history, differing only in whether the
// replay-safe wrappers sit between the global proxies and the in-memory
// exporters.
func TestReplayTelemetry(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	t.Run("WrappersSuppressReplayDuplicates", func(t *testing.T) {
		capture := newTelemetryCapture()
		tp, lp, mp := capture.providers()
		pointGlobalsAt(t,
			googleadk.NewReplaySafeTracerProvider(tp),
			googleadk.NewReplaySafeLoggerProvider(lp),
			googleadk.NewReplaySafeMeterProvider(mp),
		)

		cm, wfID, runID := runTelemetryScenario(t, c, "adk-telemetry-gated")

		// The gate must not suppress first-execution telemetry: exactly one
		// copy of every signal from the single real run.
		baseline := capture.snapshot(t)
		require.EqualValues(t, 2, cm.calls.Load(), "the scripted conversation is exactly 2 model turns")
		require.Equal(t, map[string]int{
			"generate_content fake-model": 2,
			"execute_tool get_weather":    1,
			"invoke_agent assistant":      1,
		}, baseline.spansByName)
		require.Equal(t, 2, baseline.tokenUsageSpans, "each model turn records token usage once")
		require.EqualValues(t, 250, baseline.inputTokens)
		require.EqualValues(t, 37, baseline.outputTokens)
		require.Equal(t, 2, baseline.logEvents["gen_ai.choice"])
		require.NotEmpty(t, baseline.logEvents["gen_ai.user.message"])
		require.EqualValues(t, 100, baseline.counterValue,
			"the workflow-side global-meter recording must survive the gate on first execution")

		replayTelemetryHistory(t, c, wfID, runID)

		final := capture.snapshot(t)
		require.Equal(t, baseline.spansByName, final.spansByName, "replays must add zero spans")
		require.Equal(t, baseline.tokenUsageSpans, final.tokenUsageSpans)
		require.Equal(t, baseline.inputTokens, final.inputTokens)
		require.Equal(t, baseline.outputTokens, final.outputTokens)
		require.Equal(t, baseline.logEvents, final.logEvents, "replays must add zero log events")
		require.Equal(t, baseline.counterValue, final.counterValue, "replays must add zero metric recordings")
		require.EqualValues(t, 2, cm.calls.Load(), "replays must not invoke the model")
	})

	// Control documenting the bug the wrappers fix: with the raw providers as
	// the global targets, each of the telemetryReplays replays re-emits one
	// full copy of the workflow-side telemetry while the model still ran only
	// in the real execution. Assertions compare against the replay-added delta
	// rather than a multiple of the baseline: a sticky-cache miss during the
	// live run (sticky schedule-to-start timeout on a loaded machine) triggers
	// an ungated catch-up replay that inflates the baseline itself, but each
	// replayer pass deterministically adds exactly one clean copy.
	t.Run("UngatedGlobalsInflateOnReplay", func(t *testing.T) {
		capture := newTelemetryCapture()
		tp, lp, mp := capture.providers()
		pointGlobalsAt(t, tp, lp, mp)

		cm, wfID, runID := runTelemetryScenario(t, c, "adk-telemetry-ungated")
		baseline := capture.snapshot(t)
		require.GreaterOrEqual(t, baseline.spansByName["generate_content fake-model"], 2)
		require.GreaterOrEqual(t, baseline.counterValue, int64(100))

		replayTelemetryHistory(t, c, wfID, runID)

		final := capture.snapshot(t)
		perReplay := map[string]int{
			"generate_content fake-model": 2,
			"execute_tool get_weather":    1,
			"invoke_agent assistant":      1,
		}
		for name, per := range perReplay {
			require.Equalf(t, telemetryReplays*per, final.spansByName[name]-baseline.spansByName[name],
				"span %q must re-emit on every replay without the wrappers", name)
		}
		require.Equal(t, telemetryReplays*2, final.tokenUsageSpans-baseline.tokenUsageSpans)
		require.EqualValues(t, int64(telemetryReplays)*250, final.inputTokens-baseline.inputTokens)
		require.EqualValues(t, int64(telemetryReplays)*37, final.outputTokens-baseline.outputTokens)
		require.Equal(t, telemetryReplays*2, final.logEvents["gen_ai.choice"]-baseline.logEvents["gen_ai.choice"])
		for name, base := range baseline.logEvents {
			require.Greaterf(t, final.logEvents[name], base,
				"log event %q must re-emit on replay without the wrappers", name)
		}
		require.EqualValues(t, int64(telemetryReplays)*100, final.counterValue-baseline.counterValue)
		require.EqualValues(t, 2, cm.calls.Load(), "replays must not invoke the model")
	})
}

// ----------------------------------------------------------------------------
// Gate edge cases (no dev server).
// ----------------------------------------------------------------------------

// TestReplaySafeProvidersPassThroughNonWorkflowContext: emissions whose context
// carries no workflow.Context — worker, client, Activity, or arbitrary user
// code — must delegate to the wrapped providers unchanged, so installing the
// wrappers process-wide is safe.
func TestReplaySafeProvidersPassThroughNonWorkflowContext(t *testing.T) {
	ctx := context.Background()
	capture := newTelemetryCapture()
	tp, lp, mp := capture.providers()

	sctx, span := googleadk.NewReplaySafeTracerProvider(tp).Tracer("t").Start(ctx, "plain-span")
	require.True(t, span.IsRecording(), "non-workflow spans must be real recording spans")
	require.Equal(t, span, trace.SpanFromContext(sctx))
	span.End()
	require.Len(t, capture.spans.Ended(), 1)
	require.Equal(t, "plain-span", capture.spans.Ended()[0].Name())

	logger := googleadk.NewReplaySafeLoggerProvider(lp).Logger("t")
	require.True(t, logger.Enabled(ctx, otellog.EnabledParameters{}))
	var rec otellog.Record
	rec.SetEventName("plain-event")
	logger.Emit(ctx, rec)
	require.Equal(t, 1, capture.logs.snapshot()["plain-event"])

	meter := googleadk.NewReplaySafeMeterProvider(mp).Meter("t")
	ic, err := meter.Int64Counter("ic")
	require.NoError(t, err)
	ic.Add(ctx, 1)
	iud, err := meter.Int64UpDownCounter("iud")
	require.NoError(t, err)
	iud.Add(ctx, 2)
	ih, err := meter.Int64Histogram("ih")
	require.NoError(t, err)
	ih.Record(ctx, 3)
	ig, err := meter.Int64Gauge("ig")
	require.NoError(t, err)
	ig.Record(ctx, 4)
	fc, err := meter.Float64Counter("fc")
	require.NoError(t, err)
	fc.Add(ctx, 5)
	fud, err := meter.Float64UpDownCounter("fud")
	require.NoError(t, err)
	fud.Add(ctx, 6)
	fh, err := meter.Float64Histogram("fh")
	require.NoError(t, err)
	fh.Record(ctx, 7)
	fg, err := meter.Float64Gauge("fg")
	require.NoError(t, err)
	fg.Record(ctx, 8)

	var rm metricdata.ResourceMetrics
	require.NoError(t, capture.reader.Collect(ctx, &rm))
	recorded := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			recorded[m.Name] = true
		}
	}
	for _, name := range []string{"ic", "iud", "ih", "ig", "fc", "fud", "fh", "fg"} {
		require.Truef(t, recorded[name], "sync instrument %q must record with a non-workflow context", name)
	}
}

// TestReplaySafeMeterWrapsInstrumentReturnedWithError: the OTel SDK meter
// returns a usable instrument alongside a non-nil error (e.g. an instrument
// name failing validation), and the OTel API contract lets callers keep using
// it. The wrapper must return that instrument wrapped, not nil — a nil return
// panics callers following the contract, and through the pre-delegation global
// proxy it silently turns the instrument into a permanent no-op.
func TestReplaySafeMeterWrapsInstrumentReturnedWithError(t *testing.T) {
	ctx := context.Background()
	reader := sdkmetric.NewManualReader()
	meter := googleadk.NewReplaySafeMeterProvider(
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	).Meter("t")

	// Spaces fail the SDK's instrument-name validation; the SDK still returns
	// a fully functional instrument alongside the error.
	ic, err := meter.Int64Counter("bad ic")
	require.Error(t, err)
	require.NotNil(t, ic)
	ic.Add(ctx, 1)
	iud, err := meter.Int64UpDownCounter("bad iud")
	require.Error(t, err)
	require.NotNil(t, iud)
	iud.Add(ctx, 2)
	ih, err := meter.Int64Histogram("bad ih")
	require.Error(t, err)
	require.NotNil(t, ih)
	ih.Record(ctx, 3)
	ig, err := meter.Int64Gauge("bad ig")
	require.Error(t, err)
	require.NotNil(t, ig)
	ig.Record(ctx, 4)
	fc, err := meter.Float64Counter("bad fc")
	require.Error(t, err)
	require.NotNil(t, fc)
	fc.Add(ctx, 5)
	fud, err := meter.Float64UpDownCounter("bad fud")
	require.Error(t, err)
	require.NotNil(t, fud)
	fud.Add(ctx, 6)
	fh, err := meter.Float64Histogram("bad fh")
	require.Error(t, err)
	require.NotNil(t, fh)
	fh.Record(ctx, 7)
	fg, err := meter.Float64Gauge("bad fg")
	require.Error(t, err)
	require.NotNil(t, fg)
	fg.Record(ctx, 8)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	recorded := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			recorded[m.Name] = true
		}
	}
	for _, name := range []string{"bad ic", "bad iud", "bad ih", "bad ig", "bad fc", "bad fud", "bad fh", "bad fg"} {
		require.Truef(t, recorded[name], "instrument %q returned with a name-validation error must remain usable", name)
	}
}

// TestReplaySafeMeterObservablePassthrough: observable instruments and
// RegisterCallback must come from the inner meter untouched — their callbacks
// run on the reader's collect cycle, never under a workflow context, and the
// inner meter rejects registration of instruments it did not create.
func TestReplaySafeMeterObservablePassthrough(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := googleadk.NewReplaySafeMeterProvider(
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	).Meter("t")

	obs, err := meter.Int64ObservableCounter("obs_counter")
	require.NoError(t, err)
	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(obs, 42)
		return nil
	}, obs)
	require.NoError(t, err, "the observable returned by the wrapped meter must register with the inner meter")
	defer func() { require.NoError(t, reg.Unregister()) }()

	require.EqualValues(t, 42, collectInt64Sum(t, reader, "obs_counter"))
}

// TestReplaySafeProvidersRecordDuringLiveWorkflow: a workflow.Context on the
// emission context must not suppress anything while the workflow is executing
// live (IsReplaying false) — the gate is replay-only, not workflow-only.
func TestReplaySafeProvidersRecordDuringLiveWorkflow(t *testing.T) {
	capture := newTelemetryCapture()
	tp, lp, mp := capture.providers()
	tracer := googleadk.NewReplaySafeTracerProvider(tp).Tracer("t")
	logger := googleadk.NewReplaySafeLoggerProvider(lp).Logger("t")
	counter, err := googleadk.NewReplaySafeMeterProvider(mp).Meter("t").Int64Counter("live_counter")
	require.NoError(t, err)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		adkCtx := googleadk.NewContext(ctx)
		_, span := tracer.Start(adkCtx, "live-span")
		span.End()
		var rec otellog.Record
		rec.SetEventName("live-event")
		logger.Emit(adkCtx, rec)
		counter.Add(adkCtx, 7)
		return nil
	})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Len(t, capture.spans.Ended(), 1)
	require.Equal(t, "live-span", capture.spans.Ended()[0].Name())
	require.Equal(t, 1, capture.logs.snapshot()["live-event"])
	require.EqualValues(t, 7, collectInt64Sum(t, capture.reader, "live_counter"))
}
