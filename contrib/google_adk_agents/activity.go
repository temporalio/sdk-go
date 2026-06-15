package google_adk_agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/platform"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// Activity names under which RegisterActivities registers the turn Activities.
// The workflow dispatches by these names, so users composing their own
// workflows can reference them directly.
const (
	ActivityNameRunTurn          = "RunTurnActivity"
	ActivityNameRunTurnStreaming = "RunTurnStreamingActivity"
)

// ApplicationError type strings used to classify turn failures. The workflow
// inspects ApplicationError.Type (never err.Error()) to decide how to surface
// a failure.
const (
	// ErrTypeAgentError marks a transient, retryable turn failure (model or
	// network hiccup). Temporal retries it per the configured RetryPolicy.
	ErrTypeAgentError = "ADKAgentError"
	// ErrTypeNonRetryable marks a configuration or programming error that
	// retrying cannot fix (unknown agent, bad factory, unsupported feature).
	ErrTypeNonRetryable = "ADKNonRetryable"
	// ErrTypeConfirmationRejected marks a tool whose confirmation the human
	// rejected. The turn cannot proceed and is not retried.
	ErrTypeConfirmationRejected = "ADKConfirmationRejected"
)

// ErrStatefulMCPUnsupported is returned by classification when an
// AgentFactory signals it needs a long-lived MCP session spanning turns.
// Each turn runs in an independent Activity process, so there is nowhere to
// hold a live MCP connection across turns; build a fresh McpToolset per turn
// inside the factory instead. An AgentFactory may return this error to assert
// the unsupported path; it is classified non-retryable.
var ErrStatefulMCPUnsupported = errors.New("google_adk_agents: stateful cross-turn MCP sessions are not supported (build a fresh McpToolset per turn in the factory)")

// DeterminismSeed carries workflow-deterministic values that make ADK event
// IDs and timestamps reproducible across Activity retries and continue-as-new.
type DeterminismSeed struct {
	// BaseTime is the deterministic base timestamp (typically workflow.Now).
	BaseTime time.Time
	// UUIDSeed is a deterministic prefix (typically a workflow.SideEffect
	// UUID) used to derive event identifiers.
	UUIDSeed string
}

// IsZero reports whether the seed is empty, in which case ADK falls back to
// the wall clock and random UUIDs (its default behavior outside a workflow).
func (s DeterminismSeed) IsZero() bool {
	return s.BaseTime.IsZero() && s.UUIDSeed == ""
}

// WithDeterministicProviders installs context-scoped ADK time and UUID
// providers derived from seed. Calls to platform.Now and platform.NewUUID on
// the returned context (and any derived context) — which is how
// session.NewEventWithContext stamps every event — become reproducible: a
// retried turn re-emits identical event IDs and timestamps.
//
// When seed is zero the context is returned unchanged, so an agent run
// outside Temporal keeps ADK's default wall-clock / random-UUID behavior.
//
// WithDeterministicProviders is exported so users who run their own
// Activities around an ADK call tree get the same guarantee.
func WithDeterministicProviders(ctx context.Context, seed DeterminismSeed) context.Context {
	if seed.IsZero() {
		return ctx
	}
	var mu sync.Mutex
	var timeTick int64
	var uuidCounter int64

	timeProvider := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := seed.BaseTime.Add(time.Duration(timeTick) * time.Millisecond)
		timeTick++
		return t
	}
	uuidProvider := func() string {
		mu.Lock()
		defer mu.Unlock()
		uuidCounter++
		return fmt.Sprintf("%s-%06d", seed.UUIDSeed, uuidCounter)
	}

	ctx = platform.WithTimeProvider(ctx, timeProvider)
	ctx = platform.WithUUIDProvider(ctx, uuidProvider)
	return ctx
}

// StreamTarget identifies the workflow that a streaming turn forwards partial
// events to. The workflow sets it on TurnInput; users do not fill it in.
type StreamTarget struct {
	WorkflowID string
	RunID      string
	SignalName string
}

// TurnInput is the input to a turn Activity. It carries only the agent NAME
// and serializable conversation state — never live objects or credentials.
type TurnInput struct {
	// AgentName is the key the worker-side registry reconstructs the runner
	// from.
	AgentName string
	// AppName must match the AppName the runner was built with so the session
	// lookups line up. Defaults to AgentName when empty.
	AppName string
	// UserID and SessionID identify the ADK session.
	UserID    string
	SessionID string
	// Message is the user (or tool-response) content for this turn.
	Message *genai.Content
	// PriorEvents is the durable conversation snapshot replayed into the
	// session before the turn runs.
	PriorEvents []*session.Event
	// RunConfig is the ADK run configuration (streaming mode, etc.).
	RunConfig adkagent.RunConfig
	// Seed makes event identity reproducible. The workflow derives it.
	Seed DeterminismSeed
	// StreamSignalTarget, when set, makes RunTurnStreamingActivity forward
	// partial events to the workflow. The workflow sets it.
	StreamSignalTarget *StreamTarget
}

// TurnResult is the output of a turn Activity.
type TurnResult struct {
	// Events is the full session snapshot after the turn — the durable
	// conversation record the workflow carries forward as PriorEvents.
	Events []*session.Event
	// PendingConfirmations holds tool confirmations the turn is waiting on.
	// A non-empty slice means the turn paused for human input.
	PendingConfirmations []ConfirmationRequest
	// SessionState is the ADK session state map after the turn.
	SessionState map[string]any
}

// ConfirmationRequest describes a tool call awaiting human confirmation.
type ConfirmationRequest struct {
	// FunctionCallID is the ADK function-call ID the confirmation resolves.
	FunctionCallID string
	// ToolName is the best-effort name of the tool requesting confirmation.
	ToolName string
	// Hint is the human-readable prompt explaining what is being confirmed.
	Hint string
	// Payload is any tool-supplied confirmation context.
	Payload any
}

// StreamChunk is the payload of a streaming partial-event signal.
type StreamChunk struct {
	// Index is the monotonically increasing chunk number for the turn.
	Index int
	// Text is the coalesced partial text since the previous chunk.
	Text string
	// Done marks the final chunk of a turn.
	Done bool
}

// Activities holds the worker-side dependencies the turn Activities need: the
// agent registry and the plugin options. Register it with RegisterActivities.
type Activities struct {
	reg  *AgentRegistry
	opts Options
}

// NewActivities builds the turn Activities bound to a registry and options.
func NewActivities(reg *AgentRegistry, opts Options) *Activities {
	return &Activities{reg: reg, opts: opts.withDefaults()}
}

// RunTurnActivity runs one native runner.Run turn for the named agent. It
// reconstructs the runner from the registry, seeds the prior conversation,
// installs deterministic providers, and returns the post-turn session
// snapshot. Errors are classified into ApplicationError types.
func (a *Activities) RunTurnActivity(ctx context.Context, in TurnInput) (TurnResult, error) {
	return a.runTurn(ctx, in, false)
}

// RunTurnStreamingActivity is the SSE variant of RunTurnActivity. When
// in.StreamSignalTarget is set it forwards each partial event to the parent
// workflow as a StreamChunk signal (coalesced by StreamingBatchInterval).
func (a *Activities) RunTurnStreamingActivity(ctx context.Context, in TurnInput) (TurnResult, error) {
	return a.runTurn(ctx, in, true)
}

func (a *Activities) runTurn(ctx context.Context, in TurnInput, streaming bool) (TurnResult, error) {
	logger := activity.GetLogger(ctx)

	factory, ok := a.reg.factory(in.AgentName)
	if !ok {
		return TurnResult{}, temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("no agent registered under name %q", in.AgentName),
			ErrTypeNonRetryable,
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	setup, err := factory(ctx)
	if err != nil {
		return TurnResult{}, classifyTurnError(fmt.Errorf("agent factory for %q failed: %w", in.AgentName, err))
	}
	if setup == nil || setup.Runner == nil || setup.SessionService == nil {
		return TurnResult{}, temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("agent factory for %q returned an incomplete AgentRunner; Runner and SessionService are required", in.AgentName),
			ErrTypeNonRetryable,
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}
	if setup.RequiresStatefulMCP {
		return TurnResult{}, classifyTurnError(ErrStatefulMCPUnsupported)
	}
	if a.opts.WarnOnExternalSessionService && setup.ExternalSessionService {
		logger.Warn("ADK agent uses an external session service; the Temporal-owned snapshot may diverge (double durability)",
			"agent", in.AgentName)
	}

	appName := in.AppName
	if appName == "" {
		appName = in.AgentName
	}

	if err := seedSession(ctx, setup.SessionService, appName, in); err != nil {
		return TurnResult{}, classifyTurnError(fmt.Errorf("seeding prior conversation: %w", err))
	}

	runCtx := WithDeterministicProviders(ctx, in.Seed)

	cfg := in.RunConfig
	var streamer *partialStreamer
	if streaming && in.StreamSignalTarget != nil {
		cfg.StreamingMode = adkagent.StreamingModeSSE
		signalName := in.StreamSignalTarget.SignalName
		streamer = newPartialStreamer(a.opts.StreamingBatchInterval, func(chunk StreamChunk) {
			info := activity.GetInfo(ctx)
			if err := activity.GetClient(ctx).SignalWorkflow(
				ctx, info.WorkflowExecution.ID, info.WorkflowExecution.RunID, signalName, chunk,
			); err != nil {
				activity.GetLogger(ctx).Warn("failed to forward stream partial", "error", err)
			}
		})
	}

	var turnEvents []*session.Event
	var runErr error
	for ev, evErr := range setup.Runner.Run(runCtx, in.UserID, in.SessionID, in.Message, cfg) {
		if evErr != nil {
			runErr = evErr
			break
		}
		turnEvents = append(turnEvents, ev)
		if streamer != nil && ev.Partial {
			streamer.add(partialText(ev))
		}
		activity.RecordHeartbeat(ctx, len(turnEvents))
	}
	if streamer != nil {
		streamer.flush()
	}

	pending := extractConfirmations(turnEvents)
	if runErr != nil {
		if errors.Is(runErr, tool.ErrConfirmationRequired) {
			// A confirmation request is a graceful human-in-the-loop pause,
			// not a turn failure. The workflow will await confirmation.
			logger.Info("turn paused awaiting tool confirmation",
				"agent", in.AgentName, "pendingConfirmations", len(pending))
		} else {
			return TurnResult{}, classifyTurnError(runErr)
		}
	}

	events, state, err := snapshotSession(ctx, setup.SessionService, appName, in)
	if err != nil {
		return TurnResult{}, classifyTurnError(fmt.Errorf("reading session snapshot: %w", err))
	}
	if len(events) == 0 {
		events = turnEvents
	}

	logger.Info("completed ADK turn",
		"agent", in.AgentName, "events", len(events), "pendingConfirmations", len(pending))
	return TurnResult{Events: events, PendingConfirmations: pending, SessionState: state}, nil
}

// seedSession ensures the session exists and replays prior events into it.
func seedSession(ctx context.Context, svc session.Service, appName string, in TurnInput) error {
	sess, err := getOrCreateSession(ctx, svc, appName, in.UserID, in.SessionID)
	if err != nil {
		return err
	}
	for _, ev := range in.PriorEvents {
		if ev == nil {
			continue
		}
		if err := svc.AppendEvent(ctx, sess, ev); err != nil {
			return fmt.Errorf("append prior event: %w", err)
		}
	}
	return nil
}

func getOrCreateSession(ctx context.Context, svc session.Service, appName, userID, sessionID string) (session.Session, error) {
	getResp, err := svc.Get(ctx, &session.GetRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	if err == nil && getResp != nil && getResp.Session != nil {
		return getResp.Session, nil
	}
	createResp, cerr := svc.Create(ctx, &session.CreateRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	if cerr != nil {
		return nil, fmt.Errorf("create session: %w", cerr)
	}
	return createResp.Session, nil
}

// snapshotSession reads the full event list and state after a turn.
func snapshotSession(ctx context.Context, svc session.Service, appName string, in TurnInput) ([]*session.Event, map[string]any, error) {
	getResp, err := svc.Get(ctx, &session.GetRequest{AppName: appName, UserID: in.UserID, SessionID: in.SessionID})
	if err != nil || getResp == nil || getResp.Session == nil {
		return nil, nil, nil
	}
	var events []*session.Event
	for ev := range getResp.Session.Events().All() {
		events = append(events, ev)
	}
	state := map[string]any{}
	for k, v := range getResp.Session.State().All() {
		state[k] = v
	}
	return events, state, nil
}

// extractConfirmations scans the turn's events for pending tool confirmations.
func extractConfirmations(events []*session.Event) []ConfirmationRequest {
	var out []ConfirmationRequest
	for _, ev := range events {
		if ev == nil || len(ev.Actions.RequestedToolConfirmations) == 0 {
			continue
		}
		for id, tc := range ev.Actions.RequestedToolConfirmations {
			req := ConfirmationRequest{FunctionCallID: id, Hint: tc.Hint, Payload: tc.Payload}
			if ev.Content != nil {
				for _, part := range ev.Content.Parts {
					if part.FunctionCall != nil && part.FunctionCall.ID == id {
						req.ToolName = part.FunctionCall.Name
					}
				}
			}
			out = append(out, req)
		}
	}
	return out
}

// classifyTurnError maps an arbitrary turn error onto an ApplicationError with
// a stable Type. Already-classified ApplicationErrors pass through unchanged.
func classifyTurnError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		return err
	}
	switch {
	case errors.Is(err, ErrStatefulMCPUnsupported):
		return temporal.NewApplicationErrorWithOptions(
			err.Error(), ErrTypeNonRetryable,
			temporal.ApplicationErrorOptions{NonRetryable: true, Cause: err})
	case errors.Is(err, tool.ErrConfirmationRejected):
		return temporal.NewApplicationErrorWithOptions(
			err.Error(), ErrTypeConfirmationRejected,
			temporal.ApplicationErrorOptions{NonRetryable: true, Cause: err})
	default:
		// Model/network errors are transient by default; Temporal retries
		// them per the configured RetryPolicy.
		return temporal.NewApplicationErrorWithOptions(
			err.Error(), ErrTypeAgentError,
			temporal.ApplicationErrorOptions{Cause: err})
	}
}

func partialText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range ev.Content.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// partialStreamer coalesces partial-event text and forwards it to a sink as
// StreamChunk values. Text accumulated within StreamingBatchInterval is sent as
// a single chunk; flush emits the final chunk. The sink is injected so the
// coalescing logic can be unit-tested without an Activity client.
type partialStreamer struct {
	interval time.Duration
	sink     func(StreamChunk)

	mu        sync.Mutex
	buf       strings.Builder
	index     int
	lastFlush time.Time
}

func newPartialStreamer(interval time.Duration, sink func(StreamChunk)) *partialStreamer {
	return &partialStreamer{interval: interval, sink: sink, lastFlush: time.Now()}
}

func (s *partialStreamer) add(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	s.buf.WriteString(text)
	due := time.Since(s.lastFlush) >= s.interval
	s.mu.Unlock()
	if due {
		s.emit(false)
	}
}

func (s *partialStreamer) flush() {
	s.emit(true)
}

func (s *partialStreamer) emit(done bool) {
	s.mu.Lock()
	text := s.buf.String()
	s.buf.Reset()
	idx := s.index
	s.index++
	s.lastFlush = time.Now()
	s.mu.Unlock()

	if text == "" && !done {
		return
	}
	if s.sink != nil {
		s.sink(StreamChunk{Index: idx, Text: text, Done: done})
	}
}
