package toolregistry

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// sessionCheckpoint is the data serialized to the heartbeat on each turn.
// It is unexported — callers interact only with [AgenticSession].
type sessionCheckpoint struct {
	Version  int              `json:"version"`
	Messages []map[string]any `json:"messages"`
	Issues   []map[string]any `json:"issues"`
}

// AgenticSession holds conversation state across a multi-turn LLM tool-use
// loop. On activity retry, [RunWithSession] restores the session from the last
// heartbeat checkpoint so the conversation resumes mid-turn rather than
// restarting from the beginning.
type AgenticSession struct {
	// Messages is the full conversation history. Append-only during a session.
	Messages []map[string]any
	// Issues accumulates application-level results from tool calls.
	// Elements must be JSON-serializable for checkpoint storage.
	Issues []map[string]any
}

// RunToolLoop runs the agentic tool-use loop to completion.
//
// If Messages is empty (fresh start), prompt is added as the first user
// message. Otherwise the existing conversation state is resumed (retry case).
//
// On every turn it checkpoints via [activity.RecordHeartbeat] before calling
// the LLM, then checks ctx.Err() — if the heartbeat timeout fires and the
// activity is cancelled, the context is cancelled and RunToolLoop returns
// immediately. The next attempt will restore from the last checkpoint.
func (s *AgenticSession) RunToolLoop(
	ctx context.Context,
	provider Provider,
	registry *ToolRegistry,
	system, prompt string,
) error {
	if len(s.Messages) == 0 {
		s.Messages = []map[string]any{{"role": "user", "content": prompt}}
	}

	for {
		if err := s.checkpoint(ctx); err != nil {
			return err
		}

		newMsgs, done, err := provider.RunTurn(ctx, s.Messages, registry.defs)
		if err != nil {
			return fmt.Errorf("toolregistry: session turn: %w", err)
		}
		s.Messages = append(s.Messages, newMsgs...)

		if done {
			return nil
		}
	}
}

// checkpoint heartbeats the current session state. It also returns ctx.Err()
// after recording, so callers detect heartbeat-timeout cancellation promptly.
//
// Returns a non-retryable [temporal.ApplicationError] if any issue cannot be
// JSON-marshaled — this surfaces the mistake at heartbeat time rather than
// silently losing data on the next retry.
func (s *AgenticSession) checkpoint(ctx context.Context) error {
	for i, issue := range s.Issues {
		if _, err := json.Marshal(issue); err != nil {
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf(
					"AgenticSession: issues[%d] is not JSON-serializable: %v. "+
						"Store only map[string]any with JSON-serializable values.",
					i, err),
				"InvalidArgument",
				err,
			)
		}
	}
	activity.RecordHeartbeat(ctx, sessionCheckpoint{
		Version:  1,
		Messages: s.Messages,
		Issues:   s.Issues,
	})
	return ctx.Err()
}

// RunWithSession runs fn with a durable, checkpointed LLM session.
//
// On entry it checks for heartbeat details from a prior attempt via
// [activity.GetHeartbeatDetails]. If found, the session is restored
// (messages + issues) so the conversation resumes mid-turn rather than
// restarting from turn 0.
//
// Go concurrency note: activities are goroutines with a context. Unlike Python
// and TypeScript, which require explicit async machinery, Go's blocking HTTP
// calls inside fn are naturally fine — the goroutine simply blocks until each
// LLM response arrives. Context cancellation (from heartbeat timeout or
// workflow cancellation) propagates via ctx and is checked after each
// heartbeat inside [AgenticSession.RunToolLoop].
//
// Example:
//
//	err := toolregistry.RunWithSession(ctx, func(ctx context.Context, s *toolregistry.AgenticSession) error {
//	    return s.RunToolLoop(ctx, provider, registry, system, prompt)
//	})
func RunWithSession(ctx context.Context, fn func(ctx context.Context, s *AgenticSession) error) error {
	session := &AgenticSession{
		Messages: make([]map[string]any, 0),
		Issues:   make([]map[string]any, 0),
	}

	// Attempt to restore from a previous heartbeat checkpoint.
	if activity.HasHeartbeatDetails(ctx) {
		var cp sessionCheckpoint
		if err := activity.GetHeartbeatDetails(ctx, &cp); err != nil {
			activity.GetLogger(ctx).Warn(
				"AgenticSession: failed to decode checkpoint, starting fresh",
				"error", err,
			)
		} else if cp.Version != 0 && cp.Version != 1 {
			activity.GetLogger(ctx).Warn(
				"AgenticSession: checkpoint version, expected 1 — starting fresh",
				"version", cp.Version,
			)
		} else {
			if cp.Version == 0 {
				activity.GetLogger(ctx).Warn(
					"AgenticSession: checkpoint has no version field" +
						" — may be from an older release",
				)
			}
			if cp.Messages != nil {
				session.Messages = cp.Messages
			}
			if cp.Issues != nil {
				session.Issues = cp.Issues
			}
		}
	}

	return fn(ctx, session)
}

// MarshalIssue is a convenience helper that JSON-encodes v and stores the
// result in session.Issues. Use it when your issue type is a struct rather
// than a plain map.
func MarshalIssue(s *AgenticSession, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("toolregistry: marshal issue: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("toolregistry: unmarshal issue: %w", err)
	}
	s.Issues = append(s.Issues, m)
	return nil
}
