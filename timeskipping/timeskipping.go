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
//		timeskipping.TimeSkippingConfig{Enabled: true})
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

type (
	// TimeSkipper wraps a client so that workflows started through it carry a
	// time-skipping configuration, and drives fast-forwards on running
	// workflows and schedules.
	//
	// NOTE: Experimental
	TimeSkipper = internal.TimeSkipper

	// TimeSkippingConfig is the time-skipping (v2) configuration for an
	// execution or a schedule.
	//
	// NOTE: Experimental
	TimeSkippingConfig = internal.TimeSkippingConfig

	// FastForwardConfig is a one-shot fast-forward inside a TimeSkippingConfig.
	//
	// NOTE: Experimental
	FastForwardConfig = internal.FastForwardConfig

	// FastForwardOptions configures a single fast-forward on a workflow.
	//
	// NOTE: Experimental
	FastForwardOptions = internal.TimeSkippingFastForwardOptions

	// ScheduleFastForwardOptions configures a single fast-forward on a schedule.
	//
	// NOTE: Experimental
	ScheduleFastForwardOptions = internal.ScheduleFastForwardOptions

	// TimeSkippingInfo is the server's view of time skipping on an execution or a
	// schedule.
	//
	// NOTE: Experimental
	TimeSkippingInfo = internal.TimeSkippingInfo

	// TimeSkippingFastForwardInfo is the server's view of one fast-forward inside a
	// TimeSkippingInfo.
	//
	// NOTE: Experimental
	TimeSkippingFastForwardInfo = internal.TimeSkippingFastForwardInfo
)

// NewTimeSkipper wraps c so that every workflow started through the returned
// TimeSkipper.Client is stamped with config. The namespace is required because
// it is not exposed on the client.Client interface but is needed for the
// service calls time skipping makes.
//
// NOTE: Experimental
func NewTimeSkipper(c client.Client, namespace string, config TimeSkippingConfig) (*TimeSkipper, error) {
	return internal.NewTimeSkipper(c, namespace, config)
}

// StartDevServer starts a Temporal dev server with time skipping enabled and
// returns a DevServer whose Client stamps the given Config on every workflow
// start.
//
// NOTE: Experimental
func StartDevServer(ctx context.Context, options DevServerOptions) (*DevServer, error) {
	return startDevServer(ctx, options)
}
