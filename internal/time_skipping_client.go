package internal

import (
	"sync"
	"time"
)

type (
	// TimeSkippingConfig is the time-skipping (v2) configuration for an execution or a
	// schedule. It mirrors temporal.api.common.v1.TimeSkippingConfig.
	//
	// Time skipping is a testing facility and is not intended for production workloads.
	//
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingConfig], [go.temporal.io/sdk/timeskipping.TimeSkippingConfig]
	//
	// NOTE: Experimental
	TimeSkippingConfig struct {
		// Enabled controls whether time skipping is on.
		Enabled bool

		// FastForwardConfig is the one-shot fast-forward, if any. Nil means no bounded
		// fast-forward: for a workflow that is unbounded skipping when Enabled, or no
		// skipping at all when not. A schedule has no terminal state to skip to, so the
		// server requires a bounded fast-forward on one.
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
	// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardConfig], [go.temporal.io/sdk/client.FastForwardConfig]
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
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingInfo], [go.temporal.io/sdk/timeskipping.TimeSkippingInfo]
	//
	// NOTE: Experimental
	TimeSkippingInfo struct {
		// CurrentTime is the current virtual time. An execution that has not skipped
		// any time yet reports wall-clock time here.
		CurrentTime time.Time

		// EffectiveConfig is the configuration actually in force, which can differ from
		// the one last set: defaulted fields are filled in, and Enabled goes false once
		// skipping stops — the fast-forward reached its target, MaxSessionSkipCount was
		// hit, or a client disabled it.
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
	// Exposed as: [go.temporal.io/sdk/client.TimeSkippingFastForwardInfo], [go.temporal.io/sdk/timeskipping.TimeSkippingFastForwardInfo]
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

	// TimeSkippingFastForwardOptions configures a single fast-forward on a workflow.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.FastForwardOptions]
	//
	// NOTE: Experimental
	TimeSkippingFastForwardOptions struct {
		// Duration to advance virtual time by. Zero means unbounded: enable time
		// skipping and wait for the execution to end.
		Duration time.Duration
	}

	// ScheduleFastForwardOptions configures a single fast-forward on a schedule.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.ScheduleFastForwardOptions]
	//
	// NOTE: Experimental
	ScheduleFastForwardOptions struct {
		// Duration to advance the schedule's virtual clock by. Required — a schedule
		// has no terminal state to skip to, so the server rejects unbounded time
		// skipping on one.
		Duration time.Duration
	}

	// TimeSkipper wraps a client with an interceptor that stamps a
	// TimeSkippingConfig on every workflow started through TimeSkipper.Client.
	// Callers can also FastForward a running workflow, read its virtual clock, or
	// suspend stamping for a block.
	//
	// TimeSkipper is normally accessed through a time-skipping dev server; see
	// [go.temporal.io/sdk/timeskipping.StartDevServer]. It can also be used
	// directly to drive time skipping on an existing client.
	//
	// Exposed as: [go.temporal.io/sdk/timeskipping.TimeSkipper]
	//
	// NOTE: Experimental
	TimeSkipper struct {
		client    *WorkflowClient
		namespace string

		// mu guards config and stampEnabled, which callers may mutate from one
		// goroutine while workflows are being started from another.
		mu           sync.RWMutex
		config       TimeSkippingConfig
		stampEnabled bool
	}
)
