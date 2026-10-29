// Package timeskipping drives Temporal's per-workflow and per-schedule time
// skipping (v2) against a real server.
//
// Time skipping lets a test advance an execution's virtual clock instead of
// waiting out its timers, so an hour-long sleep or a week of scheduled runs
// completes in moments. It is a testing facility and is not intended for
// production workloads, but it is driven through an ordinary client rather than
// a test framework, so it lives here rather than in testsuite.
//
// A TimeSkipper wraps a client: workflows started through TimeSkipper.Client
// carry its configuration, and FastForward advances a running workflow. For
// schedules, FastForwardSchedule advances the schedule's clock so the actions
// in that window run back-to-back.
//
// Using it against any client:
//
//	c, _ := client.Dial(client.Options{HostPort: "localhost:7233"})
//	skipper, _ := timeskipping.NewTimeSkipper(c, "default",
//		client.TimeSkippingConfig{Enabled: true})
//	ok, _ := skipper.FastForward(ctx, run, timeskipping.FastForwardOptions{Duration: time.Hour})
//
// StartDevServer starts a dev server already configured for time skipping,
// which is the easiest way to get a suitable server in a test.
//
// The server must have time skipping enabled; see StartDevServer for the
// dynamic-config values involved.
package timeskipping

import (
	"context"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
)

// The configuration and info types time skipping shares with the rest of the
// client API — TimeSkippingConfig, FastForwardConfig, TimeSkippingInfo and
// TimeSkippingFastForwardInfo — live in [go.temporal.io/sdk/client] and are
// used from there rather than re-exported here.
type (
	// TimeSkipper wraps a client so that workflows started through it carry a
	// time-skipping configuration, and drives fast-forwards on running
	// workflows and schedules.
	//
	// NOTE: Experimental
	TimeSkipper = internal.TimeSkipper

	// FastForwardOptions configures a single fast-forward on a workflow.
	//
	// NOTE: Experimental
	FastForwardOptions = internal.TimeSkippingFastForwardOptions

	// ScheduleFastForwardOptions configures a single fast-forward on a schedule.
	//
	// NOTE: Experimental
	ScheduleFastForwardOptions = internal.ScheduleFastForwardOptions

	// PollFastForwardOptions identifies the workflow fast-forward to wait for.
	//
	// NOTE: Experimental
	PollFastForwardOptions = internal.TimeSkippingPollFastForwardOptions

	// PollScheduleFastForwardOptions identifies the schedule fast-forward to wait
	// for.
	//
	// NOTE: Experimental
	PollScheduleFastForwardOptions = internal.SchedulePollFastForwardOptions

	// FastForwardPollResult is the outcome of waiting for a fast-forward. A
	// fast-forward that stops without completing is reported in FailedReason
	// rather than as an error; errors mean the call itself failed.
	//
	// NOTE: Experimental
	FastForwardPollResult = internal.FastForwardPollResult
)

// NewTimeSkipper wraps c so that every workflow started through the returned
// TimeSkipper.Client is stamped with config. The namespace is required because
// it is not exposed on the client.Client interface but is needed for the
// service calls time skipping makes.
//
// NOTE: Experimental
func NewTimeSkipper(c client.Client, namespace string, config client.TimeSkippingConfig) (*TimeSkipper, error) {
	return internal.NewTimeSkipper(c, namespace, config)
}

// PollFastForward waits for the identified fast-forward on a workflow execution
// to complete.
//
// Completion is reported in the result; a fast-forward that stops without
// completing carries the server's reason in FailedReason rather than returning
// an error, so a caller can report it. Errors mean the call itself failed.
//
// This is the wait half of TimeSkipper.FastForward on its own, for callers that
// issued the fast-forward separately and hold only a client — a CLI, where
// starting a fast-forward and waiting for it are different invocations. The wait
// is a server long poll reissued until ctx ends.
//
// NOTE: Experimental
func PollFastForward(
	ctx context.Context, c client.Client, options PollFastForwardOptions,
) (FastForwardPollResult, error) {
	return internal.PollFastForward(ctx, c, options)
}

// PollScheduleFastForward waits for the identified fast-forward on a schedule to
// complete.
//
// Completion is reported in the result; a fast-forward that stops without
// completing carries the reason in FailedReason rather than returning an error.
// Errors mean the call itself failed.
//
// This is the wait half of TimeSkipper.FastForwardSchedule on its own, for
// callers that issued the fast-forward separately. It needs no TimeSkipper — the
// schedule handle carries everything required. Schedules have no polling RPC, so
// the wait re-reads Describe on a ticker until ctx ends.
//
// NOTE: Experimental
func PollScheduleFastForward(
	ctx context.Context, handle client.ScheduleHandle, options PollScheduleFastForwardOptions,
) (FastForwardPollResult, error) {
	return internal.PollScheduleFastForward(ctx, handle, options)
}

// StartDevServer starts a Temporal dev server with time skipping enabled and
// returns a DevServer whose Client stamps the given Config on every workflow
// start.
//
// NOTE: Experimental
func StartDevServer(ctx context.Context, options DevServerOptions) (*DevServer, error) {
	return startDevServer(ctx, options)
}
