package google_adk_agents

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
)

// Default configuration values applied by Options.withDefaults.
const (
	// DefaultStreamingSignalName is the signal name the streaming Activity uses
	// to forward partial events to the parent workflow when Options does not
	// override it.
	DefaultStreamingSignalName = "__adk_stream_partial"

	defaultStartToCloseTimeout    = 5 * time.Minute
	defaultHeartbeatTimeout       = 30 * time.Second
	defaultStreamingBatchInterval = 200 * time.Millisecond
	defaultCANMaxTurns            = 250
	defaultCANMaxSessionBytes     = 2 * 1024 * 1024
)

// ActivityOptions tunes how a single turn Activity is dispatched for a given
// agent. Thinking models and long-running tools warrant longer timeouts than
// a chat agent, so these can be set per agent via Options.ActivityOptions.
type ActivityOptions struct {
	// StartToCloseTimeout bounds a single turn. Defaults to five minutes.
	StartToCloseTimeout time.Duration
	// ScheduleToCloseTimeout optionally bounds total turn time including
	// retries. Zero means unset.
	ScheduleToCloseTimeout time.Duration
	// HeartbeatTimeout is applied to the streaming Activity so a slow model
	// is not mistaken for a stuck Activity. Defaults to thirty seconds.
	HeartbeatTimeout time.Duration
	// RetryPolicy controls Temporal's turn-level retry. Temporal — not ADK —
	// owns turn retry; ADK's own in-call retries are left at their defaults.
	// Errors classified non-retryable (see the ADK* error types) are never
	// retried regardless of this policy.
	RetryPolicy *temporal.RetryPolicy
	// SummaryFunc produces the single-line Activity summary shown in the
	// Temporal UI/CLI. Defaults to the agent name.
	SummaryFunc func(TurnInput) string
}

// CANThreshold controls when AgentSessionWorkflow continue-as-news. The first
// threshold crossed triggers the boundary. A zero field disables that trigger.
type CANThreshold struct {
	// MaxTurns triggers continue-as-new once this many turns have run.
	MaxTurns int
	// MaxSessionBytes triggers continue-as-new once the JSON-encoded session
	// snapshot reaches this size.
	MaxSessionBytes int
}

// Options configures the worker-side Activities and the AgentSessionWorkflow.
type Options struct {
	// DefaultActivityOptions applies to every agent that has no entry in
	// ActivityOptions.
	DefaultActivityOptions ActivityOptions
	// ActivityOptions holds per-agent overrides keyed by the registered agent
	// name. Unset fields fall back to DefaultActivityOptions.
	ActivityOptions map[string]ActivityOptions
	// StreamingSignalName is the signal the streaming Activity sends partial
	// events on. Defaults to DefaultStreamingSignalName.
	StreamingSignalName string
	// StreamingBatchInterval coalesces partial events emitted within the
	// interval into a single signal. Defaults to 200ms.
	StreamingBatchInterval time.Duration
	// ContinueAsNewThreshold controls continue-as-new. Defaults to 250 turns
	// or 2 MiB of session snapshot.
	ContinueAsNewThreshold CANThreshold
	// WarnOnExternalSessionService logs a warning when an agent is configured
	// with a non-in-memory ADK session service, which can diverge from the
	// Temporal-owned snapshot (double durability).
	WarnOnExternalSessionService bool
}

// withDefaults returns a copy of o with unset fields populated.
func (o Options) withDefaults() Options {
	if o.StreamingSignalName == "" {
		o.StreamingSignalName = DefaultStreamingSignalName
	}
	if o.StreamingBatchInterval <= 0 {
		o.StreamingBatchInterval = defaultStreamingBatchInterval
	}
	if o.ContinueAsNewThreshold.MaxTurns == 0 {
		o.ContinueAsNewThreshold.MaxTurns = defaultCANMaxTurns
	}
	if o.ContinueAsNewThreshold.MaxSessionBytes == 0 {
		o.ContinueAsNewThreshold.MaxSessionBytes = defaultCANMaxSessionBytes
	}
	o.DefaultActivityOptions = o.DefaultActivityOptions.withDefaults()
	return o
}

// withDefaults fills unset timeout fields.
func (a ActivityOptions) withDefaults() ActivityOptions {
	if a.StartToCloseTimeout <= 0 {
		a.StartToCloseTimeout = defaultStartToCloseTimeout
	}
	if a.HeartbeatTimeout <= 0 {
		a.HeartbeatTimeout = defaultHeartbeatTimeout
	}
	return a
}

// Validate reports configuration that would fail at runtime.
func (o Options) Validate() error {
	if o.StreamingBatchInterval < 0 {
		return fmt.Errorf("google_adk_agents: StreamingBatchInterval must not be negative")
	}
	if o.ContinueAsNewThreshold.MaxTurns < 0 || o.ContinueAsNewThreshold.MaxSessionBytes < 0 {
		return fmt.Errorf("google_adk_agents: ContinueAsNewThreshold fields must not be negative")
	}
	for name, ao := range o.ActivityOptions {
		if ao.StartToCloseTimeout < 0 || ao.ScheduleToCloseTimeout < 0 || ao.HeartbeatTimeout < 0 {
			return fmt.Errorf("google_adk_agents: ActivityOptions[%q] has a negative timeout", name)
		}
	}
	return nil
}

// activityOptionsFor merges the per-agent override over the default options.
func (o Options) activityOptionsFor(agentName string) ActivityOptions {
	merged := o.DefaultActivityOptions
	if override, ok := o.ActivityOptions[agentName]; ok {
		if override.StartToCloseTimeout > 0 {
			merged.StartToCloseTimeout = override.StartToCloseTimeout
		}
		if override.ScheduleToCloseTimeout > 0 {
			merged.ScheduleToCloseTimeout = override.ScheduleToCloseTimeout
		}
		if override.HeartbeatTimeout > 0 {
			merged.HeartbeatTimeout = override.HeartbeatTimeout
		}
		if override.RetryPolicy != nil {
			merged.RetryPolicy = override.RetryPolicy
		}
		if override.SummaryFunc != nil {
			merged.SummaryFunc = override.SummaryFunc
		}
	}
	return merged.withDefaults()
}

// Summary produces the Activity summary for a turn, defaulting to the agent
// name when no SummaryFunc is configured. RunTurn calls it to set the
// per-invocation Temporal UI summary; it is exported so users composing their
// own workflows can reuse the same defaulting logic.
func (a ActivityOptions) Summary(in TurnInput) string {
	if a.SummaryFunc != nil {
		if s := a.SummaryFunc(in); s != "" {
			return s
		}
	}
	return in.AgentName
}
