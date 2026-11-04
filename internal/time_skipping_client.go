package internal

import (
	"time"
)

type (
	// TimeSkippingConfig is the time-skipping (v2) configuration for an execution or a
	// schedule. It mirrors temporal.api.common.v1.TimeSkippingConfig.
	//
	// Time skipping is a testing facility and is not intended for production workloads.
	//
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingConfig]
	//
	// NOTE: Experimental
	TimeSkippingConfig struct {
		// Enabled controls whether time skipping is on.
		Enabled bool

		// FastForwardConfig is the one-shot fast-forward, if any. Nil means no bounded
		// fast-forward.
		FastForwardConfig *FastForwardConfig

		// DisablePropagation stops the enabled flag from propagating to executions started
		// by this one — a child workflow, or a workflow started by a schedule. Virtual
		// start time still propagates.
		DisablePropagation bool

		// MaxSessionSkipCount caps the number of skips within one time-skipping session,
		// which runs from one configuration update to the next. Reaching it disables time
		// skipping. Zero means use the server default.
		MaxSessionSkipCount int32
	}

	// FastForwardConfig is a one-shot fast-forward inside a [TimeSkippingConfig]. Both
	// fields are required whenever a fast-forward is configured.
	//
	// Exposed as: [go.temporal.io/sdk/client.FastForwardConfig]
	//
	// NOTE: Experimental
	FastForwardConfig struct {
		// ID identifies this fast-forward when waiting for it to complete.
		ID string

		// Duration advances virtual time by this amount. Reaching the resulting target
		// time completes the fast-forward and disables time skipping.
		Duration time.Duration
	}

	// TimeSkippingInfo is the server's view of time skipping on an execution or a
	// schedule.
	//
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingInfo]
	//
	// NOTE: Experimental
	TimeSkippingInfo struct {
		// CurrentTime is the current virtual time. An execution that has not skipped
		// any time yet reports wall-clock time here.
		CurrentTime time.Time

		// EffectiveConfig is the configuration actually in force, which can differ from
		// the one last set: defaulted fields are filled in, and Enabled goes false once
		// skipping stops.
		EffectiveConfig *TimeSkippingConfig

		// FastForwardInfo is the current fast-forward, if any. The server leaves it in
		// place after completion, so check HasCompleted rather than treating a non-nil
		// value as one still in flight.
		FastForwardInfo *TimeSkippingFastForwardInfo

		// CurrentSessionSkipCount is the number of skips accumulated in the current
		// session, bounded by MaxSessionSkipCount. It resets whenever that cap changes.
		CurrentSessionSkipCount int32
	}

	// TimeSkippingFastForwardInfo is the server's view of one fast-forward.
	//
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingFastForwardInfo]
	//
	// NOTE: Experimental
	TimeSkippingFastForwardInfo struct {
		// ID is the fast-forward's client-supplied identifier.
		ID string

		// Duration is the client-supplied amount of virtual time to advance by.
		Duration time.Duration

		// TargetTime is the virtual time at which the fast-forward completes.
		TargetTime time.Time

		// HasCompleted reports whether TargetTime has been reached.
		HasCompleted bool
	}

	// WorkflowFastForwardOptions configures a single fast-forward on a workflow.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.WorkflowFastForwardOptions]
	//
	// NOTE: Experimental
	WorkflowFastForwardOptions struct {
		// Duration to advance virtual time by. Must not be negative; the server
		// requires a positive fast-forward duration.
		//
		// Zero is a client-side convention for unbounded: no fast-forward is sent
		// at all, time skipping is simply enabled and the call waits for the
		// execution to end.
		Duration time.Duration
	}

	// ScheduleFastForwardOptions configures a single fast-forward on a schedule.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.ScheduleFastForwardOptions]
	//
	// NOTE: Experimental
	ScheduleFastForwardOptions struct {
		// Duration to advance the schedule's virtual clock by. Must be positive.
		//
		// Zero is not "unbounded" here as it is for a workflow: a schedule has no
		// terminal state to skip to, and the server requires a fast-forward
		// whenever time skipping is enabled on a schedule.
		Duration time.Duration
	}

	// FastForwardPollResult is the outcome of waiting for a fast-forward.
	//
	// A fast-forward that stops without completing is reported here rather than as
	// an error: it is an outcome of the wait, not a failure of the call.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardPollResult]
	//
	// NOTE: Experimental
	FastForwardPollResult struct {
		// Completed is true when the fast-forward reached its target time.
		Completed bool

		// FailedReason says why the fast-forward stopped without completing, and is
		// empty when Completed is true. For a workflow it is the server's own
		// failed_reason; for a schedule, which has no polling RPC, it is derived
		// from the schedule's reported state.
		FailedReason string
	}

	// WorkflowPollFastForwardOptions identifies the workflow fast-forward to
	// wait for.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.WorkflowPollFastForwardOptions]
	//
	// NOTE: Experimental
	WorkflowPollFastForwardOptions struct {
		// FastForwardID of the fast-forward to wait for, as set in
		// FastForwardConfig.ID. Required.
		FastForwardID string
	}

	// SchedulePollFastForwardOptions identifies the schedule fast-forward to wait
	// for.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.SchedulePollFastForwardOptions]
	//
	// NOTE: Experimental
	SchedulePollFastForwardOptions struct {
		// FastForwardID of the fast-forward to wait for, as set in
		// FastForwardConfig.ID. Required.
		FastForwardID string
	}
)
