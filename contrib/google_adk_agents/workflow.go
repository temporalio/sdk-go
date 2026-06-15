package google_adk_agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Signal, query, and update names handled by AgentSessionWorkflow. External
// callers reference these to drive a session.
const (
	// SignalSendMessage enqueues a user turn. Payload: TurnRequest. Fire-and-
	// forget; a turn that fails after Temporal's retries are exhausted fails the
	// workflow with a classified ApplicationError (see newWorkflowFailure).
	SignalSendMessage = "send_message"
	// SignalConfirm resolves a pending human-in-the-loop tool confirmation.
	// Payload: ToolConfirmationDecision.
	SignalConfirm = "confirm"
	// QueryConversation returns the full durable transcript ([]*session.Event).
	QueryConversation = "conversation"
	// QueryPendingConfirmations returns tool confirmations the latest turn is
	// awaiting ([]ConfirmationRequest).
	QueryPendingConfirmations = "pending_confirmations"
	// QueryPendingStream returns the partial StreamChunks accumulated for the
	// in-flight (or most recent) streaming turn.
	QueryPendingStream = "pending_stream"
	// UpdateSendMessageAndWait sends a user turn and waits for its TurnResult.
	// Its validator rejects an empty message. Payload: TurnRequest.
	UpdateSendMessageAndWait = "send_message_and_wait"
	// UpdateConfirm resolves a pending tool confirmation and waits for the
	// resulting TurnResult. Payload: ToolConfirmationDecision.
	UpdateConfirm = "confirm_and_wait"
)

// TurnRequest is the payload of SignalSendMessage and UpdateSendMessageAndWait.
type TurnRequest struct {
	// Message is the user content for the turn. Must be non-empty.
	Message *genai.Content
}

// ToolConfirmationDecision resolves a pending tool confirmation. It is turned
// into an ADK function-response message that re-enters ADK's native
// confirmation-resume path on the next turn.
type ToolConfirmationDecision struct {
	// FunctionCallID is the ID from the matching ConfirmationRequest.
	FunctionCallID string
	// ToolName is the tool the confirmation belongs to (echoed back to ADK).
	ToolName string
	// Confirmed is the human's decision: true approves the tool call.
	Confirmed bool
	// Payload is any extra confirmation context the tool expects.
	Payload any
}

// TurnSettings is the serializable per-turn configuration carried in
// AgentSessionInput. It excludes the function-valued knobs on ActivityOptions
// (SummaryFunc) because those cannot survive continue-as-new serialization;
// the bundled workflow uses the agent name as the Activity summary. Use
// NewAgentSessionInput to derive TurnSettings from Options.
type TurnSettings struct {
	// StartToCloseTimeout bounds one turn Activity. Defaults to five minutes.
	StartToCloseTimeout time.Duration
	// ScheduleToCloseTimeout optionally bounds a turn including retries.
	ScheduleToCloseTimeout time.Duration
	// HeartbeatTimeout applies to streaming turns. Defaults to thirty seconds.
	HeartbeatTimeout time.Duration
	// RetryPolicy is Temporal's turn-level retry policy.
	RetryPolicy *temporal.RetryPolicy
	// Streaming selects RunTurnStreamingActivity so partial events stream back
	// to the workflow as StreamChunk signals.
	Streaming bool
	// StreamingSignalName is the signal partial events arrive on. Must match
	// the worker Options.StreamingSignalName. Defaults to
	// DefaultStreamingSignalName.
	StreamingSignalName string
	// ContinueAsNewThreshold controls when the workflow continue-as-news.
	ContinueAsNewThreshold CANThreshold
}

func (s TurnSettings) withDefaults() TurnSettings {
	if s.StartToCloseTimeout <= 0 {
		s.StartToCloseTimeout = defaultStartToCloseTimeout
	}
	if s.HeartbeatTimeout <= 0 {
		s.HeartbeatTimeout = defaultHeartbeatTimeout
	}
	if s.StreamingSignalName == "" {
		s.StreamingSignalName = DefaultStreamingSignalName
	}
	if s.ContinueAsNewThreshold.MaxTurns == 0 {
		s.ContinueAsNewThreshold.MaxTurns = defaultCANMaxTurns
	}
	if s.ContinueAsNewThreshold.MaxSessionBytes == 0 {
		s.ContinueAsNewThreshold.MaxSessionBytes = defaultCANMaxSessionBytes
	}
	return s
}

// toActivityOptions converts the serializable settings into the ActivityOptions
// RunTurn consumes. SummaryFunc is intentionally nil so Summary defaults to the
// agent name.
func (s TurnSettings) toActivityOptions() ActivityOptions {
	return ActivityOptions{
		StartToCloseTimeout:    s.StartToCloseTimeout,
		ScheduleToCloseTimeout: s.ScheduleToCloseTimeout,
		HeartbeatTimeout:       s.HeartbeatTimeout,
		RetryPolicy:            s.RetryPolicy,
	}.withDefaults()
}

// ResumeState carries conversation state across a continue-as-new boundary.
// Callers starting a fresh session leave it nil.
type ResumeState struct {
	// PriorEvents is the durable transcript carried forward.
	PriorEvents []*session.Event
	// SessionState is the ADK session state map carried forward.
	SessionState map[string]any
	// TurnCount is the number of turns already run, preserved so
	// continue-as-new does not reset the threshold counter.
	TurnCount int
}

// AgentSessionInput starts an AgentSessionWorkflow.
type AgentSessionInput struct {
	// AgentName is the registered agent the worker reconstructs per turn.
	AgentName string
	// AppName matches the runner's app name; defaults to AgentName.
	AppName string
	// UserID and SessionID identify the ADK session.
	UserID    string
	SessionID string
	// Settings holds the serializable per-turn configuration.
	Settings TurnSettings
	// ResumeState is non-nil only on a continue-as-new continuation.
	ResumeState *ResumeState
}

// NewAgentSessionInput builds an AgentSessionInput from worker Options,
// resolving per-agent ActivityOptions and continue-as-new thresholds into the
// serializable TurnSettings the workflow carries. Set Settings.Streaming
// afterward to enable token streaming.
func NewAgentSessionInput(opts Options, agentName, appName, userID, sessionID string) AgentSessionInput {
	opts = opts.withDefaults()
	ao := opts.activityOptionsFor(agentName)
	return AgentSessionInput{
		AgentName: agentName,
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Settings: TurnSettings{
			StartToCloseTimeout:    ao.StartToCloseTimeout,
			ScheduleToCloseTimeout: ao.ScheduleToCloseTimeout,
			HeartbeatTimeout:       ao.HeartbeatTimeout,
			RetryPolicy:            ao.RetryPolicy,
			StreamingSignalName:    opts.StreamingSignalName,
			ContinueAsNewThreshold: opts.ContinueAsNewThreshold,
		},
	}
}

// newWorkflowFailure builds the terminal workflow failure raised when a
// signal-driven turn fails after Temporal's retries are exhausted (or fails
// non-retryably). It re-surfaces the turn's classified ApplicationError Type as
// the OUTERMOST ApplicationError so a caller doing a single
// errors.As(&temporal.ApplicationError{}) reads the classified type directly
// (e.g. ErrTypeNonRetryable), while the original error stays attached as the
// cause. A custom Go error struct is intentionally avoided: it would be
// flattened to a meaningless type name by Temporal's failure converter on the
// way to external callers, who only ever observe an ApplicationError.
func newWorkflowFailure(cause error) error {
	typ := ErrTypeAgentError
	var appErr *temporal.ApplicationError
	if errors.As(cause, &appErr) && appErr.Type() != "" {
		typ = appErr.Type()
	}
	return temporal.NewApplicationErrorWithOptions(
		"google_adk_agents: agent turn failed",
		typ,
		temporal.ApplicationErrorOptions{Cause: cause, NonRetryable: true},
	)
}

// sessionRuntime holds the mutable per-execution state shared across the main
// loop and the signal/update handler coroutines. The Temporal Go dispatcher is
// cooperative (no preemption between yield points), so this plain struct is
// safe without locks; a workflow Channel serializes the turns themselves.
type sessionRuntime struct {
	priorEvents          []*session.Event
	sessionState         map[string]any
	pendingConfirmations []ConfirmationRequest
	streamChunks         []StreamChunk
	turnCount            int
	active               int
	fatalErr             error
}

// AgentSessionWorkflow is the durable multi-turn agent session. It owns the
// conversation transcript, runs each turn as a RunTurnActivity, exposes the
// transcript and pending confirmations via queries, accepts new turns via
// signal or update, resolves human-in-the-loop confirmations, surfaces
// streamed partials, and continue-as-news when a threshold is crossed.
func AgentSessionWorkflow(ctx workflow.Context, in AgentSessionInput) error {
	logger := workflow.GetLogger(ctx)
	in.Settings = in.Settings.withDefaults()

	state := &sessionRuntime{sessionState: map[string]any{}}
	if in.ResumeState != nil {
		state.priorEvents = in.ResumeState.PriorEvents
		if in.ResumeState.SessionState != nil {
			state.sessionState = in.ResumeState.SessionState
		}
		state.turnCount = in.ResumeState.TurnCount
		logger.Info("resumed agent session after continue-as-new",
			"turnCount", state.turnCount, "priorEvents", len(state.priorEvents))
	}

	if err := workflow.SetQueryHandler(ctx, QueryConversation, func() ([]*session.Event, error) {
		return state.priorEvents, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, QueryPendingConfirmations, func() ([]ConfirmationRequest, error) {
		return state.pendingConfirmations, nil
	}); err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, QueryPendingStream, func() ([]StreamChunk, error) {
		return state.streamChunks, nil
	}); err != nil {
		return err
	}

	// lock is a one-token mutex serializing turn execution; wake nudges the
	// main loop to re-evaluate continue-as-new after a turn completes.
	lock := workflow.NewBufferedChannel(ctx, 1)
	lock.Send(ctx, nil)
	wake := workflow.NewBufferedChannel(ctx, 64)

	// startTurn spawns a guarded turn coroutine and returns its result future.
	// active is incremented synchronously (before any yield) so the main loop
	// never observes a just-scheduled turn as idle.
	startTurn := func(msg *genai.Content) workflow.Future {
		future, settable := workflow.NewFuture(ctx)
		state.active++
		workflow.Go(ctx, func(gctx workflow.Context) {
			defer func() {
				state.active--
				wake.SendAsync(nil)
			}()
			lock.Receive(gctx, nil)
			defer lock.Send(gctx, nil)
			res, err := state.runTurn(gctx, in, msg)
			settable.Set(res, err)
		})
		return future
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateSendMessageAndWait,
		func(uctx workflow.Context, req TurnRequest) (TurnResult, error) {
			var res TurnResult
			err := startTurn(req.Message).Get(uctx, &res)
			return res, err
		},
		workflow.UpdateHandlerOptions{Validator: func(_ workflow.Context, req TurnRequest) error {
			return validateMessage(req.Message)
		}},
	); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateConfirm,
		func(uctx workflow.Context, dec ToolConfirmationDecision) (TurnResult, error) {
			var res TurnResult
			err := startTurn(confirmationMessage(dec)).Get(uctx, &res)
			return res, err
		},
		workflow.UpdateHandlerOptions{Validator: func(_ workflow.Context, dec ToolConfirmationDecision) error {
			if dec.FunctionCallID == "" {
				return fmt.Errorf("google_adk_agents: ToolConfirmationDecision.FunctionCallID is required")
			}
			return nil
		}},
	); err != nil {
		return err
	}

	sendCh := workflow.GetSignalChannel(ctx, SignalSendMessage)
	confirmCh := workflow.GetSignalChannel(ctx, SignalConfirm)
	streamCh := workflow.GetSignalChannel(ctx, in.Settings.StreamingSignalName)

	for {
		if state.fatalErr != nil {
			return newWorkflowFailure(state.fatalErr)
		}
		if state.canContinueAsNew(in.Settings.ContinueAsNewThreshold) &&
			sendCh.Len() == 0 && confirmCh.Len() == 0 {
			logger.Info("continue-as-new: carrying session snapshot forward",
				"turnCount", state.turnCount, "events", len(state.priorEvents))
			return workflow.NewContinueAsNewError(ctx, AgentSessionWorkflow, AgentSessionInput{
				AgentName: in.AgentName,
				AppName:   in.AppName,
				UserID:    in.UserID,
				SessionID: in.SessionID,
				Settings:  in.Settings,
				ResumeState: &ResumeState{
					PriorEvents:  state.priorEvents,
					SessionState: state.sessionState,
					TurnCount:    state.turnCount,
				},
			})
		}

		sel := workflow.NewSelector(ctx)
		sel.AddReceive(sendCh, func(c workflow.ReceiveChannel, _ bool) {
			var req TurnRequest
			c.Receive(ctx, &req)
			if err := validateMessage(req.Message); err != nil {
				logger.Warn("ignoring invalid send_message signal", "error", err)
				return
			}
			f := startTurn(req.Message)
			workflow.Go(ctx, func(gctx workflow.Context) {
				var res TurnResult
				if err := f.Get(gctx, &res); err != nil {
					state.fatalErr = err
					wake.SendAsync(nil)
				}
			})
		})
		sel.AddReceive(confirmCh, func(c workflow.ReceiveChannel, _ bool) {
			var dec ToolConfirmationDecision
			c.Receive(ctx, &dec)
			f := startTurn(confirmationMessage(dec))
			workflow.Go(ctx, func(gctx workflow.Context) {
				var res TurnResult
				if err := f.Get(gctx, &res); err != nil {
					state.fatalErr = err
					wake.SendAsync(nil)
				}
			})
		})
		sel.AddReceive(streamCh, func(c workflow.ReceiveChannel, _ bool) {
			var chunk StreamChunk
			c.Receive(ctx, &chunk)
			state.streamChunks = append(state.streamChunks, chunk)
		})
		sel.AddReceive(wake, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
		})
		sel.Select(ctx)
	}
}

// runTurn dispatches one turn Activity and folds its result into the durable
// state. It derives a DeterminismSeed from workflow-deterministic values so
// event identity is reproducible across Activity retries and continue-as-new.
func (s *sessionRuntime) runTurn(ctx workflow.Context, in AgentSessionInput, msg *genai.Content) (TurnResult, error) {
	s.turnCount++
	seed := DeterminismSeed{
		BaseTime: workflow.Now(ctx),
		UUIDSeed: fmt.Sprintf("%s-%d", workflow.GetInfo(ctx).WorkflowExecution.RunID, s.turnCount),
	}

	turnIn := TurnInput{
		AgentName:   in.AgentName,
		AppName:     in.AppName,
		UserID:      in.UserID,
		SessionID:   in.SessionID,
		Message:     msg,
		PriorEvents: s.priorEvents,
		RunConfig:   adkagent.RunConfig{},
		Seed:        seed,
	}
	if in.Settings.Streaming {
		s.streamChunks = nil
		turnIn.StreamSignalTarget = &StreamTarget{SignalName: in.Settings.StreamingSignalName}
	}

	res, err := RunTurn(ctx, turnIn, in.Settings.toActivityOptions())
	if err != nil {
		return TurnResult{}, err
	}
	s.priorEvents = res.Events
	s.sessionState = res.SessionState
	s.pendingConfirmations = res.PendingConfirmations
	return res, nil
}

// canContinueAsNew reports whether a continue-as-new threshold is crossed and
// the session is at a clean boundary (no in-flight turn, no pending human
// confirmation).
func (s *sessionRuntime) canContinueAsNew(t CANThreshold) bool {
	if s.active > 0 || len(s.pendingConfirmations) > 0 {
		return false
	}
	if t.MaxTurns > 0 && s.turnCount >= t.MaxTurns {
		return true
	}
	if t.MaxSessionBytes > 0 {
		if b, err := json.Marshal(s.priorEvents); err == nil && len(b) >= t.MaxSessionBytes {
			return true
		}
	}
	return false
}

// RunTurn executes a single turn Activity from workflow code: it selects the
// streaming or non-streaming Activity based on in.StreamSignalTarget, applies
// the timeouts/retry/summary from opts, and returns the TurnResult. It is
// exported so users composing their own workflows can dispatch turns without
// reaching for the Activity-name constants. opts may be zero; defaults are
// applied.
func RunTurn(ctx workflow.Context, in TurnInput, opts ActivityOptions) (TurnResult, error) {
	opts = opts.withDefaults()
	name := ActivityNameRunTurn
	if in.StreamSignalTarget != nil {
		name = ActivityNameRunTurnStreaming
	}
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    opts.StartToCloseTimeout,
		ScheduleToCloseTimeout: opts.ScheduleToCloseTimeout,
		HeartbeatTimeout:       opts.HeartbeatTimeout,
		RetryPolicy:            opts.RetryPolicy,
		Summary:                opts.Summary(in),
	})
	var res TurnResult
	err := workflow.ExecuteActivity(actCtx, name, in).Get(actCtx, &res)
	return res, err
}

// validateMessage rejects an empty user message at the workflow boundary.
func validateMessage(msg *genai.Content) error {
	if msg == nil || len(msg.Parts) == 0 {
		return fmt.Errorf("google_adk_agents: message must contain at least one part")
	}
	return nil
}

// confirmationMessage builds the ADK function-response content that resumes a
// paused tool confirmation. ADK matches it to the requested confirmation by
// FunctionCall ID and continues its native confirmation-resume path.
func confirmationMessage(dec ToolConfirmationDecision) *genai.Content {
	resp := map[string]any{"confirmed": dec.Confirmed}
	if dec.Payload != nil {
		resp["payload"] = dec.Payload
	}
	part := &genai.Part{FunctionResponse: &genai.FunctionResponse{
		ID:       dec.FunctionCallID,
		Name:     dec.ToolName,
		Response: resp,
	}}
	return genai.NewContentFromParts([]*genai.Part{part}, genai.RoleUser)
}
