package googleadk

import (
	"context"
	"errors"
	"iter"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/contrib/workflowstreams"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// StreamServer installs the workflow-side stream server that streamed model
// output lands in. Call it once, near the top of the workflow that drives
// runner.Run, whenever you set Options.StreamingTopic. The InvokeModel Activity
// publishes each model chunk back to this workflow (via a Temporal signal), and
// external consumers (UIs) read the chunks with workflowstreams.Client.Subscribe.
// Without it the published chunks have nowhere to land. It is a no-op to omit
// when streaming is disabled; the durable agent loop does not depend on it.
func StreamServer(ctx workflow.Context) error {
	_, err := workflowstreams.NewWorkflowStream(ctx, nil)
	return err
}

// TemporalModel is a model.LLM that makes an ADK agent's model calls durable:
// its GenerateContent dispatches to the InvokeModel Activity instead of calling
// a real model inside the workflow. Set it as your agent's Model; the real
// model.LLM is reconstructed worker-side by a ModelFactory (see Config.Models /
// NewActivities) keyed by the model name. Only the model name crosses into the
// workflow — credentials never leave the worker.
type TemporalModel struct {
	name                   string
	activityOptions        workflow.ActivityOptions
	summaryFn              func(*model.LLMRequest) string
	streamingTopic         string
	streamingBatchInterval time.Duration
}

// ModelOption customizes a TemporalModel.
type ModelOption func(*TemporalModel)

// WithModelActivityOptions sets the base Activity options for the InvokeModel
// Activity (StartToCloseTimeout, RetryPolicy, TaskQueue, ...). A zero
// StartToCloseTimeout defaults to two minutes.
func WithModelActivityOptions(o workflow.ActivityOptions) ModelOption {
	return func(m *TemporalModel) { m.activityOptions = o }
}

// WithModelSummary sets a function that computes the Temporal UI summary for the
// model Activity from its request. When unset the summary is the model name.
func WithModelSummary(fn func(*model.LLMRequest) string) ModelOption {
	return func(m *TemporalModel) { m.summaryFn = fn }
}

// WithStreaming makes the InvokeModel Activity call the model in streaming mode
// and publish each chunk to the given workflowstreams topic for external (UI)
// consumers; the aggregated final response is still returned into the workflow
// so replay stays deterministic. Call StreamServer(ctx) once in the workflow
// when you use this. batchInterval coalesces published chunks (zero uses the
// library default). Streaming installs a 30-second HeartbeatTimeout default
// (override via WithModelActivityOptions); InvokeModel heartbeats on a timer
// derived from the heartbeat timeout, so a model that is slow to produce its
// first chunk is not falsely timed out.
func WithStreaming(topic string, batchInterval time.Duration) ModelOption {
	return func(m *TemporalModel) {
		m.streamingTopic = topic
		m.streamingBatchInterval = batchInterval
	}
}

// NewModel returns a TemporalModel for the given model name. Use it as the Model
// on your llmagent.Config; the matching real model is built worker-side by a
// ModelFactory registered in NewActivities, by the application-owned ADK model
// registry (model.Register), or — for gemini-* names neither of those knows —
// by a built-in zero-config Gemini fallback.
func NewModel(name string, opts ...ModelOption) *TemporalModel {
	m := &TemporalModel{name: name}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Name reports the model name. ADK copies it into LLMRequest.Model, which the
// InvokeModel Activity uses to resolve the worker-side model.
func (m *TemporalModel) Name() string { return m.name }

// GenerateContent dispatches the model call to the InvokeModel Activity and
// yields the (aggregated) response. It runs on the workflow side; the real model
// never executes in-workflow. The stream argument from ADK is honored via the
// TemporalModel's own streaming configuration (WithStreaming): a single
// aggregated response is always returned into the workflow for replay safety,
// while chunks (if streaming) are published to the workflowstreams topic.
func (m *TemporalModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		wfCtx, ok := workflowContext(ctx)
		if !ok {
			yield(nil, errMissingContext)
			return
		}
		ao := m.activityOptions
		if ao.StartToCloseTimeout == 0 {
			ao.StartToCloseTimeout = defaultModelTimeout
		}
		// Streamed calls default to a 30s heartbeat timeout so a stuck worker is
		// detected well before the StartToCloseTimeout. InvokeModel heartbeats on
		// a timer derived from the heartbeat timeout (not per chunk), so a model
		// that legitimately takes longer than this to produce its first chunk is
		// not falsely timed out and retried.
		if m.streamingTopic != "" && ao.HeartbeatTimeout == 0 {
			ao.HeartbeatTimeout = defaultStreamHeartbeat
		}
		ao.Summary = m.modelSummary(req)
		actx := workflow.WithActivityOptions(wfCtx, ao)

		in := invokeModelInput{
			Request:                req,
			Stream:                 m.streamingTopic != "",
			StreamingTopic:         m.streamingTopic,
			StreamingBatchInterval: m.streamingBatchInterval,
		}
		var resp model.LLMResponse
		if err := workflow.ExecuteActivity(actx, InvokeModelActivityName, in).Get(wfCtx, &resp); err != nil {
			yield(nil, err)
			return
		}
		yield(&resp, nil)
	}
}

func (m *TemporalModel) modelSummary(req *model.LLMRequest) string {
	if m.summaryFn != nil {
		return m.summaryFn(req)
	}
	return "InvokeModel: " + m.name
}

// invokeModelInput is the serializable payload for the InvokeModel Activity. The
// live tool references inside Request.Tools are tagged json:"-" by ADK and are
// dropped automatically; only the model name and serialized tool declarations
// cross the wire.
type invokeModelInput struct {
	// Request is the ADK LLM request. Request.Model selects the worker-side
	// ModelFactory; Request.Config carries the tool declarations the model needs.
	Request *model.LLMRequest
	// Stream selects streaming mode (model called with stream=true, chunks
	// published to StreamingTopic). The aggregated final response is still
	// returned so replay stays deterministic.
	Stream bool
	// StreamingTopic is the workflowstreams topic external consumers subscribe to.
	StreamingTopic string
	// StreamingBatchInterval coalesces published chunks. Zero uses the library
	// default.
	StreamingBatchInterval time.Duration
}

// InvokeModel runs a single LLM round-trip worker-side. It reconstructs the
// model from the registered ModelFactory keyed by Request.Model, calls it, and
// returns the (aggregated) response. The user's model.LLM is therefore never
// invoked inside the workflow. Underlying model-SDK retries should be disabled
// in the factory so Temporal's retry policy is the single source of truth;
// transient failures are surfaced as retryable ApplicationErrors.
func (a *Activities) InvokeModel(ctx context.Context, in invokeModelInput) (*model.LLMResponse, error) {
	log := activity.GetLogger(ctx)
	if in.Request == nil {
		return nil, newApplicationError(ErrorTypeModel, false, nil, "InvokeModel: nil request")
	}
	// The Activity's HeartbeatTimeout (the streaming default, or one the user set
	// themselves) counts from Activity start, and a model can legitimately stay
	// silent past it before its first output (thinking models, large-context
	// prefill). Heartbeat on a timer for the whole call — model resolution (which
	// runs user factory code) and both branches — so a healthy-but-slow call is
	// not heartbeat-timed-out and retried, which would duplicate a paid model call.
	stopHeartbeats := startPeriodicHeartbeats(ctx)
	defer stopHeartbeats()

	// Resolve the model worker-side: an explicit ModelFactory (custom credentials,
	// disabled SDK retries, etc.) wins; otherwise the application-owned ADK model
	// registry, then the built-in zero-config gemini-* fallback (see
	// resolveModel). Mirrors adk-python's LLMRegistry.
	llm, err := a.resolveModel(ctx, in.Request.Model)
	if err != nil {
		return nil, newApplicationError(ErrorTypeModel, false, err,
			"resolve model %q (add it to Config.Models or register its provider): %v", in.Request.Model, err)
	}

	if in.Stream && in.StreamingTopic != "" {
		log.Debug("invoking model in streaming mode", "model", in.Request.Model, "topic", in.StreamingTopic)
		return a.invokeModelStreaming(ctx, llm, in)
	}

	log.Debug("invoking model", "model", in.Request.Model)
	var agg *model.LLMResponse
	for resp, gerr := range llm.GenerateContent(ctx, in.Request, false) {
		if gerr != nil {
			return nil, classifyModelError(gerr)
		}
		agg = aggregateResponses(agg, resp)
	}
	if agg == nil {
		return nil, newApplicationError(ErrorTypeModel, true, nil,
			"model %q returned no response", in.Request.Model)
	}
	return agg, nil
}

// startPeriodicHeartbeats records Activity heartbeats on a fixed interval until
// the returned stop function is called. The interval is a third of the
// Activity's HeartbeatTimeout; when no heartbeat timeout is set it does
// nothing. A model can legitimately take longer than the heartbeat timeout to
// produce its first output (thinking models, large-context prefill), so
// heartbeating only on chunk arrival would let the server time out and retry a
// healthy — and paid — model call. The SDK throttles outgoing heartbeats to
// 80% of the heartbeat timeout, so the short interval does not spam the server.
func startPeriodicHeartbeats(ctx context.Context) (stop func()) {
	timeout := activity.GetInfo(ctx).HeartbeatTimeout
	if timeout <= 0 {
		return func() {}
	}
	interval := timeout / 3
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			// Heartbeat first so the window opens covered: the timeout counts from
			// Activity start, not from the first recorded heartbeat.
			activity.RecordHeartbeat(ctx)
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	// stop joins the goroutine so no heartbeat can race past Activity completion.
	return func() { close(done); <-stopped }
}

// invokeModelStreaming calls the model with stream=true, publishes each chunk to
// the workflowstreams topic for external (UI) consumers, and returns the
// aggregated final response into the workflow. Liveness heartbeating is handled
// by the caller's periodic heartbeater (see InvokeModel), not per chunk.
func (a *Activities) invokeModelStreaming(ctx context.Context, llm model.LLM, in invokeModelInput) (*model.LLMResponse, error) {
	log := activity.GetLogger(ctx)
	opts := workflowstreams.Options{}
	if in.StreamingBatchInterval > 0 {
		opts.BatchInterval = in.StreamingBatchInterval
	}
	// The streaming client publishes chunks to external (UI) consumers and needs
	// a live Temporal server. If it cannot be constructed (e.g. in a unit-test
	// environment without a server), we degrade gracefully: chunks are not
	// published, but the aggregated final response is still returned into the
	// workflow, so the agent loop and replay stay correct.
	var topic *workflowstreams.TopicHandle
	if wsc, cerr := workflowstreams.NewClientFromActivity(ctx, opts); cerr != nil {
		log.Warn("streaming client unavailable; not publishing chunks", "error", cerr)
	} else {
		defer func() { _ = wsc.Close(ctx) }()
		topic = wsc.Topic(in.StreamingTopic)
	}

	var agg *model.LLMResponse
	for resp, gerr := range llm.GenerateContent(ctx, in.Request, true) {
		if gerr != nil {
			return nil, classifyModelError(gerr)
		}
		if topic != nil {
			topic.Publish(resp, false)
		}
		agg = aggregateResponses(agg, resp)
	}
	if agg == nil {
		return nil, newApplicationError(ErrorTypeModel, true, nil,
			"model %q returned no streamed response", in.Request.Model)
	}
	// Mark the aggregate complete: it is the single response handed back into the
	// workflow regardless of how many chunks were streamed to the topic.
	agg.Partial = false
	agg.TurnComplete = true
	return agg, nil
}

// aggregateResponses folds streamed responses into one. A NON-partial response
// is authoritative for content: per ADK's contract (model.LLMResponse.Partial)
// the runner fully processes only non-partial events, and Gemini's streaming
// iterator ends with a non-partial response already carrying the fully
// aggregated text and function calls — so its content REPLACES anything
// accumulated from partial display chunks, and a stream carrying several
// non-partial responses keeps every part. Partial chunks folding onto a
// partial-built aggregate concatenate text so the returned response carries the
// full message even when the model never sends a terminal aggregate. For every
// metadata field the latest chunk that sets it wins — all of them are folded
// (not just usage/finish/grounding) because a model commonly emits citations,
// logprobs, transcriptions, custom metadata, or an error/finish only on a later
// chunk, and Gemini's terminal aggregate omits fields (e.g. ModelVersion) that
// earlier chunks carried. (Partial/TurnComplete are managed by the streaming
// caller; internally agg.Partial tracks whether the aggregate is partial-built.)
func aggregateResponses(agg, next *model.LLMResponse) *model.LLMResponse {
	if next == nil {
		return agg
	}
	if agg == nil {
		// Deep-copy on the first fold so later appendText/metadata folds never
		// mutate the model's own response buffer. A shallow struct copy would
		// leave cp.Content (and its Parts) aliased to next.Content, so the next
		// chunk's appendText would corrupt the source ("a" -> "ab"). The copy
		// retains next.Partial, which then tracks whether the aggregate was
		// built from partial chunks.
		cp := *next
		cp.Content = cloneContent(next.Content)
		return &cp
	}
	switch {
	case next.Content == nil:
		// Nothing content-wise; metadata still folds below.
	case !next.Partial && agg.Partial:
		// Authoritative response supersedes content accumulated from partial
		// display chunks: folding it in would duplicate the text and drop
		// function-call parts. Metadata still folds below, so fields the
		// terminal response omits (Gemini's aggregate carries no ModelVersion)
		// survive from earlier chunks. Deep copy: the caller mutates the
		// aggregate afterwards, and the published chunks alias the original.
		agg.Content = cloneContent(next.Content)
		agg.Partial = false
	case !next.Partial:
		// A further authoritative response in the same stream (a model that
		// yields complete responses without marking partials): keep every part,
		// mirroring how ADK's flow fully processes each non-partial event.
		agg.Content = appendParts(agg.Content, next.Content)
	case agg.Partial:
		// Partial chunk onto a partial-built aggregate: concatenate text. Once
		// the aggregate is authoritative, later partials are display-only and
		// their content is ignored (no case matches).
		if agg.Content == nil {
			// Clone rather than alias: a later chunk's appendText must not
			// reach back into the model's buffer.
			agg.Content = cloneContent(next.Content)
		} else if text := concatText(next.Content); text != "" {
			appendText(agg.Content, text)
		}
	}
	if next.UsageMetadata != nil {
		agg.UsageMetadata = next.UsageMetadata
	}
	if next.GroundingMetadata != nil {
		agg.GroundingMetadata = next.GroundingMetadata
	}
	if next.FinishReason != "" {
		agg.FinishReason = next.FinishReason
	}
	if next.ModelVersion != "" {
		agg.ModelVersion = next.ModelVersion
	}
	if next.CitationMetadata != nil {
		agg.CitationMetadata = next.CitationMetadata
	}
	if next.LogprobsResult != nil {
		agg.LogprobsResult = next.LogprobsResult
	}
	if next.CustomMetadata != nil {
		agg.CustomMetadata = next.CustomMetadata
	}
	if next.InputTranscription != nil {
		agg.InputTranscription = next.InputTranscription
	}
	if next.OutputTranscription != nil {
		agg.OutputTranscription = next.OutputTranscription
	}
	if next.AvgLogprobs != 0 {
		agg.AvgLogprobs = next.AvgLogprobs
	}
	if next.ErrorCode != "" {
		agg.ErrorCode = next.ErrorCode
	}
	if next.ErrorMessage != "" {
		agg.ErrorMessage = next.ErrorMessage
	}
	if next.Interrupted {
		agg.Interrupted = true
	}
	if next.SessionResumptionHandle != "" {
		agg.SessionResumptionHandle = next.SessionResumptionHandle
	}
	return agg
}

// cloneContent returns a deep-enough copy of c for streaming aggregation: a
// fresh *genai.Content with a fresh Parts slice whose first text part can be
// appended to without touching the source. The Part structs are copied by value
// so mutating cp's text part leaves the model's buffer untouched.
func cloneContent(c *genai.Content) *genai.Content {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Parts != nil {
		cp.Parts = make([]*genai.Part, len(c.Parts))
		for i, p := range c.Parts {
			if p == nil {
				continue
			}
			pc := *p
			cp.Parts[i] = &pc
		}
	}
	return &cp
}

// appendParts appends deep-enough copies of next's parts onto agg (a clone of
// next when agg is nil): each non-partial response is a complete event in ADK's
// contract, so a stream carrying several keeps all their parts.
func appendParts(agg, next *genai.Content) *genai.Content {
	if next == nil {
		return agg
	}
	if agg == nil {
		return cloneContent(next)
	}
	agg.Parts = append(agg.Parts, cloneContent(next).Parts...)
	return agg
}

func concatText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range c.Parts {
		if p != nil {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func appendText(c *genai.Content, text string) {
	for _, p := range c.Parts {
		// Append onto the first text part to keep one coherent message.
		if p != nil && p.FunctionCall == nil && p.FunctionResponse == nil {
			p.Text += text
			return
		}
	}
	c.Parts = append(c.Parts, &genai.Part{Text: text})
}

// classifyModelError maps a model-call failure to Temporal's retry contract. A
// genai.APIError is classified by HTTP status (408/409/429 and 5xx retryable,
// other 4xx non-retryable); anything without a status is treated as a
// retryable transient fault.
func classifyModelError(err error) error {
	code := apiErrorCode(err)
	retryable := true
	if code != 0 {
		retryable = code == 408 || code == 409 || code == 429 || (code >= 500 && code < 600)
	}
	return newApplicationError(ErrorTypeModel, retryable, err, "model call failed: %v", err)
}

// apiErrorCode extracts the HTTP status from a genai.APIError, which the SDK
// returns sometimes by value and sometimes by pointer.
func apiErrorCode(err error) int {
	var ptr *genai.APIError
	if errors.As(err, &ptr) && ptr != nil {
		return ptr.Code
	}
	var val genai.APIError
	if errors.As(err, &val) {
		return val.Code
	}
	return 0
}
