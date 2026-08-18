package googleadk_test

// Probes for the otel-v2-aligned tracer wrapper (real spans during replay, End
// suppressed while replaying, replay-stable IDs from workflow random streams):
//
//   - TestSpanIDsDeterministicAcrossReplay: replays re-create every workflow
//     span — parallel tool fan-out included — with the identity it had on first
//     execution, and export none of them.
//   - TestStraddleSpanExportedOnceComplete: a span open across a sticky-cache
//     eviction (agent parked on InvokeModel) is exported exactly once, by the
//     catch-up replay's live End, complete with its gen_ai.usage.* token
//     attributes, original IDs, and a workflow-time start. Eviction teardown
//     exports nothing: the SDK marks teardown as replay before running
//     coroutine defers, so ADK's deferred force-End is suppressed.
//   - TestStraddleSpanLegacyControl: the same scenario under the previous
//     wrapper design (non-recording spans from Start while replaying),
//     reproduced verbatim in this file. It documents what the alignment
//     changes: the eviction force-End exported the still-open spans truncated
//     — generate_content without its token attributes — and the catch-up
//     recreation, being non-recording, could never complete them.

import (
	"context"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"go.temporal.io/sdk/contrib/googleadk"
)

// ----------------------------------------------------------------------------
// Span-start observation. Replay-created spans are real but never re-exported,
// so their identity is observable only at SpanProcessor.OnStart.
// ----------------------------------------------------------------------------

type startRecord struct {
	Name    string
	TraceID trace.TraceID
	SpanID  trace.SpanID
	Parent  trace.SpanID
}

type startRecordingProcessor struct {
	mu   sync.Mutex
	recs []startRecord
}

func (p *startRecordingProcessor) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs = append(p.recs, startRecord{
		Name:    s.Name(),
		TraceID: s.SpanContext().TraceID(),
		SpanID:  s.SpanContext().SpanID(),
		Parent:  s.Parent().SpanID(),
	})
}

func (p *startRecordingProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (p *startRecordingProcessor) Shutdown(context.Context) error   { return nil }
func (p *startRecordingProcessor) ForceFlush(context.Context) error { return nil }

func (p *startRecordingProcessor) len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.recs)
}

func (p *startRecordingProcessor) from(i int) []startRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]startRecord(nil), p.recs[i:]...)
}

// startSet collapses records to a set: a mid-run sticky-cache miss on a loaded
// machine replays live history and re-records the same identities, so ordered
// comparison would be flaky for the live phase while set comparison is exact.
func startSet(recs []startRecord) map[string]map[startRecord]bool {
	set := map[string]map[startRecord]bool{}
	for _, r := range recs {
		if set[r.Name] == nil {
			set[r.Name] = map[startRecord]bool{}
		}
		set[r.Name][r] = true
	}
	return set
}

// byName returns the ended spans with the given name.
func byName(spans []sdktrace.ReadOnlySpan, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

func intAttr(s sdktrace.ReadOnlySpan, key string) (int64, bool) {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsInt64(), true
		}
	}
	return 0, false
}

// newIDGeneratedCapture builds a tracer provider carrying the workflow span-ID
// generator, an OnStart recorder, and an exported-span recorder.
func newIDGeneratedCapture() (*sdktrace.TracerProvider, *startRecordingProcessor, *tracetest.SpanRecorder) {
	starts := &startRecordingProcessor{}
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithIDGenerator(googleadk.NewWorkflowSpanIDGenerator()),
		sdktrace.WithSpanProcessor(starts),
		sdktrace.WithSpanProcessor(spans),
	)
	return tp, starts, spans
}

// ----------------------------------------------------------------------------
// Deterministic IDs across replay, parallel fan-out included.
// ----------------------------------------------------------------------------

func TestSpanIDsDeterministicAcrossReplay(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	tp, starts, spans := newIDGeneratedCapture()
	capture := newTelemetryCapture()
	_, lp, mp := capture.providers()
	pointGlobalsAt(t,
		googleadk.NewReplaySafeTracerProvider(tp),
		googleadk.NewReplaySafeLoggerProvider(lp),
		googleadk.NewReplaySafeMeterProvider(mp),
	)

	// Turn 1 fans out to two parallel tools (the concurrent TaskRunner path);
	// turn 2 is the final text. Both carry token usage.
	turn1 := reproFnCalls("tool_a", "tool_b")
	turn1.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 25, TotalTokenCount: 125,
	}
	turn2 := googleadk.TextResponse("done")
	turn2.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 150, CandidatesTokenCount: 12, TotalTokenCount: 162,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	const taskQueue = "adk-otelv2-deterministic-ids"
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(agentRunWorkflow)
	acts, err := googleadk.NewActivities(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{"fake-model": scriptedModelFactory(turn1, turn2)},
	})
	require.NoError(t, err)
	acts.Register(w)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        taskQueue + "-" + time.Now().Format("150405.000"),
		TaskQueue: taskQueue,
	}, agentRunWorkflow, runInput{
		ModelName:   "fake-model",
		UserMessage: "fan out",
		Tools:       []toolSpec{{Name: "tool_a", Description: "a"}, {Name: "tool_b", Description: "b"}},
	})
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))

	liveStarts := starts.from(0)
	liveEnded := spans.Ended()
	endedByName := map[string]int{}
	for _, s := range liveEnded {
		endedByName[s.Name()]++
	}
	require.Equal(t, map[string]int{
		"invoke_agent assistant":      1,
		"generate_content fake-model": 2,
		"execute_tool tool_a":         1,
		"execute_tool tool_b":         1,
		"execute_tool (merged)":       1,
	}, endedByName, "one live execution exports each workflow span once")

	// Every replayer pass must re-create the same spans with the same IDs, in
	// the same order (deterministic coroutine scheduling), and export nothing.
	replayCtx, replayCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer replayCancel()
	var firstPass []startRecord
	for i := 0; i < telemetryReplays; i++ {
		before := starts.len()
		replayer := worker.NewWorkflowReplayer()
		replayer.RegisterWorkflow(agentRunWorkflow)
		require.NoError(t, replayer.ReplayWorkflowExecution(replayCtx, c.WorkflowService(), nil, "default", workflow.Execution{
			ID:    run.GetID(),
			RunID: run.GetRunID(),
		}))
		pass := starts.from(before)
		require.NotEmpty(t, pass, "replay must re-create the workflow spans")
		if i == 0 {
			firstPass = pass
		} else {
			require.Equal(t, firstPass, pass, "replay passes must re-create spans deterministically, order included")
		}
	}

	require.Equal(t, startSet(liveStarts), startSet(firstPass),
		"replay must reproduce the live execution's span identities (trace ID, span ID, parent), fan-out spans included")
	require.Len(t, spans.Ended(), len(liveEnded), "replays must export nothing")
}

// ----------------------------------------------------------------------------
// Straddle harness: a single model call blocked mid-Activity while the
// workflow worker is stopped, the sticky cache purged, and a fresh workflow
// worker started; the model is then released so the catch-up replay finishes
// the run live. The Activity worker survives throughout.
// ----------------------------------------------------------------------------

// gatedModel blocks the first GenerateContent until released, signalling the
// test when the call has started (which implies the span-opening workflow task
// completed and the InvokeModel Activity is running).
type gatedModel struct {
	*googleadk.FakeModel
	mu      sync.Mutex
	gated   bool
	started chan struct{}
	release chan struct{}
}

func newGatedModel(responses ...*model.LLMResponse) *gatedModel {
	return &gatedModel{
		FakeModel: googleadk.NewFakeModel(responses...),
		gated:     true,
		started:   make(chan struct{}, 1),
		release:   make(chan struct{}),
	}
}

func (m *gatedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	m.mu.Lock()
	first := m.gated
	m.gated = false
	m.mu.Unlock()
	if first {
		m.started <- struct{}{}
		<-m.release
	}
	return m.FakeModel.GenerateContent(ctx, req, stream)
}

// legacyWfCtxKey lets straddleAgentWorkflow expose its workflow.Context to the
// in-file replica of the previous wrapper design (the real key is unexported).
type legacyWfCtxKey struct{}

// straddleAgentWorkflow is a single-model-call agent run. It mirrors the
// harness runAgent but additionally stashes the workflow.Context under
// legacyWfCtxKey for the legacy-control tracer replica; the shipped wrappers
// ignore that key.
func straddleAgentWorkflow(ctx workflow.Context, in telemetryRunInput) (telemetryRunResult, error) {
	root, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Description: "straddle assistant",
		Model:       googleadk.NewModel(in.ModelName),
		Instruction: "be helpful",
	})
	if err != nil {
		return telemetryRunResult{}, err
	}
	r, err := runner.New(runner.Config{
		AppName:           "straddle-app",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return telemetryRunResult{}, err
	}

	adkCtx := context.WithValue(googleadk.NewContext(ctx), legacyWfCtxKey{}, ctx)
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

type straddleHarness struct {
	t        *testing.T
	c        client.Client
	ctx      context.Context
	tq       string
	gm       *gatedModel
	wfWorker worker.Worker
	run      client.WorkflowRun
}

// startStraddle boots the split workers (Activity-only and workflow-only) and
// starts straddleAgentWorkflow, returning once the model call is blocked
// mid-Activity with the agent's spans open.
func startStraddle(t *testing.T, c client.Client, taskQueue string) *straddleHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	turn := googleadk.TextResponse("It is sunny.")
	turn.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 25, TotalTokenCount: 125,
	}
	h := &straddleHarness{t: t, c: c, ctx: ctx, tq: taskQueue, gm: newGatedModel(turn)}

	aw := worker.New(c, taskQueue, worker.Options{DisableWorkflowWorker: true})
	acts, err := googleadk.NewActivities(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			"fake-model": func(context.Context, string) (model.LLM, error) { return h.gm, nil },
		},
	})
	require.NoError(t, err)
	acts.Register(aw)
	require.NoError(t, aw.Start())
	t.Cleanup(aw.Stop)

	h.startWorkflowWorker()
	t.Cleanup(func() {
		if h.wfWorker != nil {
			h.wfWorker.Stop()
		}
	})

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        taskQueue + "-" + time.Now().Format("150405.000"),
		TaskQueue: taskQueue,
	}, straddleAgentWorkflow, telemetryRunInput{ModelName: "fake-model", UserMessage: "weather?"})
	require.NoError(t, err)
	h.run = run

	select {
	case <-h.gm.started:
	case <-ctx.Done():
		t.Fatal("model call did not start before the harness timeout")
	}
	return h
}

func (h *straddleHarness) startWorkflowWorker() {
	h.t.Helper()
	w := worker.New(h.c, h.tq, worker.Options{
		LocalActivityWorkerOnly:      true,
		StickyScheduleToStartTimeout: time.Second,
	})
	w.RegisterWorkflow(straddleAgentWorkflow)
	require.NoError(h.t, w.Start())
	h.wfWorker = w
}

// evict stops the workflow worker and purges the process-global sticky cache,
// destroying the cached execution: its coroutines unwind via Goexit, running
// ADK's deferred span force-Ends. The Activity worker keeps the blocked model
// call alive. Purging with the Activity worker running is safe: only workflow
// task processing touches the cache (same pattern as the query-probe harness).
func (h *straddleHarness) evict() {
	h.t.Helper()
	h.wfWorker.Stop()
	h.wfWorker = nil
	worker.PurgeStickyWorkflowCache()
	h.startWorkflowWorker()
}

// finish releases the model and waits for the workflow to complete via the
// catch-up replay on the fresh worker.
func (h *straddleHarness) finish() {
	h.t.Helper()
	close(h.gm.release)
	var res telemetryRunResult
	require.NoError(h.t, h.run.Get(h.ctx, &res))
}

// TestStraddleSpanExportedOnceComplete: with the aligned wrapper, a span open
// across an eviction is exported exactly once — by the catch-up replay's live
// End — with its token attributes, its first-execution identity, and a
// workflow-time start. Nothing is exported at eviction teardown, and a
// subsequent replayer pass re-creates the same identities without exporting.
func TestStraddleSpanExportedOnceComplete(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	tp, starts, spans := newIDGeneratedCapture()
	capture := newTelemetryCapture()
	_, lp, mp := capture.providers()
	pointGlobalsAt(t,
		googleadk.NewReplaySafeTracerProvider(tp),
		googleadk.NewReplaySafeLoggerProvider(lp),
		googleadk.NewReplaySafeMeterProvider(mp),
	)

	h := startStraddle(t, c, "adk-otelv2-straddle")

	liveStarts := starts.from(0)
	require.NotEmpty(t, liveStarts, "the live phase must have opened the agent spans")
	require.Empty(t, spans.Ended(), "no span ends before the model returns")

	evictWall := time.Now()
	h.evict()
	h.finish()

	// The catch-up replay and live completion take well over the microseconds
	// the async eviction teardown needs, so any teardown export would be
	// visible by now; the legacy control below proves this observation window
	// catches them.
	ended := spans.Ended()
	gcSpans := byName(ended, "generate_content fake-model")
	agentSpans := byName(ended, "invoke_agent assistant")
	require.Len(t, gcSpans, 1, "the straddled model span must be exported exactly once — no truncated eviction copy")
	require.Len(t, agentSpans, 1, "the straddled agent span must be exported exactly once")

	gc, agent := gcSpans[0], agentSpans[0]

	// Straddle fix: the one export carries the token usage ADK attaches when
	// the model call returns (lost entirely under the legacy design).
	in, ok := intAttr(gc, "gen_ai.usage.input_tokens")
	require.True(t, ok, "the straddled generate_content span must carry gen_ai.usage.input_tokens")
	require.EqualValues(t, 100, in)
	out, ok := intAttr(gc, "gen_ai.usage.output_tokens")
	require.True(t, ok)
	require.EqualValues(t, 25, out)

	// Identity: the catch-up re-creation drew the same IDs the live run drew,
	// so the export stitches into the original trace with a valid parent.
	liveIdentity := startSet(liveStarts)
	require.True(t, liveIdentity["generate_content fake-model"][startRecord{
		Name:    "generate_content fake-model",
		TraceID: gc.SpanContext().TraceID(),
		SpanID:  gc.SpanContext().SpanID(),
		Parent:  gc.Parent().SpanID(),
	}], "the exported straddle span must keep its live identity")
	require.True(t, liveIdentity["invoke_agent assistant"][startRecord{
		Name:    "invoke_agent assistant",
		TraceID: agent.SpanContext().TraceID(),
		SpanID:  agent.SpanContext().SpanID(),
		Parent:  agent.Parent().SpanID(),
	}], "the exported agent span must keep its live identity")
	require.Equal(t, agent.SpanContext().SpanID(), gc.Parent().SpanID(),
		"the model span must still parent under the agent span")

	// Timestamps: replay-created spans start at workflow time (the original
	// task), not at replay wall time; ends are live wall-clock.
	require.True(t, gc.StartTime().Before(evictWall),
		"the catch-up re-creation must keep a workflow-time start (before the eviction), got %v", gc.StartTime())
	require.True(t, gc.EndTime().After(evictWall),
		"the straddled span must end live after the eviction")

	// A replayer pass over the finished history re-creates every span with the
	// same identity — the random stream continued correctly across the
	// mid-history replay-to-live transition — and exports nothing.
	before := starts.len()
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(straddleAgentWorkflow)
	require.NoError(t, replayer.ReplayWorkflowExecution(h.ctx, c.WorkflowService(), nil, "default", workflow.Execution{
		ID:    h.run.GetID(),
		RunID: h.run.GetRunID(),
	}))
	require.Equal(t, liveIdentity, startSet(starts.from(before)),
		"a full replay must reproduce the identities of spans created on both sides of the eviction")
	require.Len(t, spans.Ended(), len(ended), "the replayer pass must export nothing")
}

// ----------------------------------------------------------------------------
// Workflow task retry: the one remaining re-export vector. A task that fails
// after a span was started, ended, and exported re-executes live on retry and
// exports the span again — at-least-once, the same caveat all point telemetry
// carries. The alignment does not change the count; it makes the copies
// identical: the retry re-draws the same stream bytes, so both exports share
// one span ID (ID-deduplicating backends collapse them), where the legacy
// design exported two unrelated IDs.
// ----------------------------------------------------------------------------

func taskRetrySpanWorkflow(ctx workflow.Context, failFirstAttempt bool) error {
	adkCtx := context.WithValue(googleadk.NewContext(ctx), legacyWfCtxKey{}, ctx)
	_, span := otel.Tracer("retry-probe").Start(adkCtx, "retry-span")
	span.End()
	if failFirstAttempt && retryProbeArmed.CompareAndSwap(true, false) {
		panic("intentional workflow task failure")
	}
	return nil
}

var retryProbeArmed atomic.Bool

func TestTaskRetryReexportsSpan(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	runProbe := func(t *testing.T, taskQueue string, tp trace.TracerProvider) []sdktrace.ReadOnlySpan {
		t.Helper()
		capture := newTelemetryCapture()
		_, lp, mp := capture.providers()
		pointGlobalsAt(t, tp,
			googleadk.NewReplaySafeLoggerProvider(lp),
			googleadk.NewReplaySafeMeterProvider(mp),
		)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		t.Cleanup(cancel)
		w := worker.New(c, taskQueue, worker.Options{})
		w.RegisterWorkflow(taskRetrySpanWorkflow)
		require.NoError(t, w.Start())
		t.Cleanup(w.Stop)

		retryProbeArmed.Store(true)
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        taskQueue + "-" + time.Now().Format("150405.000"),
			TaskQueue: taskQueue,
		}, taskRetrySpanWorkflow, true)
		require.NoError(t, err)
		require.NoError(t, run.Get(ctx, nil))
		return nil
	}

	t.Run("AlignedCopiesShareOneSpanID", func(t *testing.T) {
		tp, _, spans := newIDGeneratedCapture()
		runProbe(t, "adk-otelv2-task-retry-aligned", googleadk.NewReplaySafeTracerProvider(tp))
		ended := byName(spans.Ended(), "retry-span")
		require.Len(t, ended, 2, "both live attempts export the span (at-least-once)")
		require.Equal(t, ended[0].SpanContext().SpanID(), ended[1].SpanContext().SpanID(),
			"the retry re-draws the same stream bytes, so the copies dedupe by span ID")
	})

	t.Run("LegacyCopiesHaveUnrelatedIDs", func(t *testing.T) {
		spans := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
		runProbe(t, "adk-otelv2-task-retry-legacy", legacyReplaySafeTracerProvider{TracerProvider: tp})
		ended := byName(spans.Ended(), "retry-span")
		require.Len(t, ended, 2, "the legacy design also exports once per live attempt")
		require.NotEqual(t, ended[0].SpanContext().SpanID(), ended[1].SpanContext().SpanID(),
			"legacy random IDs make the copies unrelated, so no backend can dedupe them")
	})
}

// ----------------------------------------------------------------------------
// Legacy control: the previous wrapper design, replicated verbatim (modulo the
// context key, which is unexported), run through the same straddle scenario.
// ----------------------------------------------------------------------------

var legacySuppressedTracer = tracenoop.NewTracerProvider().Tracer("googleadk.replay-suppressed")

type legacyReplaySafeTracerProvider struct{ trace.TracerProvider }

func (p legacyReplaySafeTracerProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return legacyReplaySafeTracer{Tracer: p.TracerProvider.Tracer(name, opts...)}
}

type legacyReplaySafeTracer struct{ trace.Tracer }

func (t legacyReplaySafeTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if wfCtx, ok := ctx.Value(legacyWfCtxKey{}).(workflow.Context); ok &&
		workflow.IsReplaying(wfCtx) && !workflow.IsReadOnly(wfCtx) {
		return legacySuppressedTracer.Start(ctx, spanName, opts...)
	}
	return t.Tracer.Start(ctx, spanName, opts...)
}

// TestStraddleSpanLegacyControl documents the behavior the alignment replaces:
// eviction teardown force-ends the still-open live spans through their raw
// handles, exporting each once, truncated — generate_content without its
// gen_ai.usage.* attributes — and the catch-up replay re-creates them
// non-recording, so they can never be completed or re-exported.
func TestStraddleSpanLegacyControl(t *testing.T) {
	c, stop := devServer(t)
	defer stop()

	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	capture := newTelemetryCapture()
	_, lp, mp := capture.providers()
	pointGlobalsAt(t,
		legacyReplaySafeTracerProvider{TracerProvider: tp},
		googleadk.NewReplaySafeLoggerProvider(lp),
		googleadk.NewReplaySafeMeterProvider(mp),
	)

	h := startStraddle(t, c, "adk-otelv2-straddle-legacy")
	require.Empty(t, spans.Ended(), "no span ends before the model returns")

	h.evict()

	// The eviction force-End exports the truncated spans; wait for the async
	// teardown to flush them. This bound also calibrates the positive test's
	// observation window.
	require.Eventually(t, func() bool {
		return len(byName(spans.Ended(), "generate_content fake-model")) == 1 &&
			len(byName(spans.Ended(), "invoke_agent assistant")) == 1
	}, 10*time.Second, 50*time.Millisecond,
		"legacy eviction teardown must export each still-open span once, truncated")

	_, hasTokens := intAttr(byName(spans.Ended(), "generate_content fake-model")[0], "gen_ai.usage.input_tokens")
	require.False(t, hasTokens, "the truncated legacy export precedes the model result, so it has no token attributes")

	h.finish()

	// The catch-up re-creation was non-recording: completion adds no exports
	// and the token attributes are lost for good.
	gcSpans := byName(spans.Ended(), "generate_content fake-model")
	require.Len(t, gcSpans, 1, "legacy catch-up must not re-export the straddled span")
	_, hasTokens = intAttr(gcSpans[0], "gen_ai.usage.input_tokens")
	require.False(t, hasTokens, "legacy design loses the straddled span's token attributes")
	require.Len(t, byName(spans.Ended(), "invoke_agent assistant"), 1)
}
