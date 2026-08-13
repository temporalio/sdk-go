package testsuite

import (
	"context"
	"errors"
	"fmt"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
)

// DevServerV2Options configures a v2 time-skipping dev server. Same shape as
// DevServerOptions plus an initial TimeSkippingConfig stamped on every
// workflow started through the returned server's Client.
//
// WARNING: Per-workflow time skipping (v2) is currently experimental.
type DevServerV2Options struct {
	DevServerOptions

	// TSConfig is the initial TimeSkippingConfig stamped on every workflow
	// started through DevServerV2.Client. Zero value means Enabled=true with
	// no bounded fast-forward.
	TSConfig TimeSkippingConfig
}

// DevServerV2 is a dev-server process with per-workflow time skipping enabled.
// It embeds the underlying DevServer and adds a TimeSkipper that stamps a
// TimeSkippingConfig on every workflow start, plus the FastForward /
// GetTimeSkippingInfo / GetCurrentTime / WithTimeSkippingDisabled operations
// that drive and observe a running workflow's virtual clock.
//
// WARNING: Per-workflow time skipping (v2) is currently experimental.
type DevServerV2 struct {
	*DevServer
	skipper *TimeSkipper
}

// StartDevServerV2 starts a Temporal dev server with per-workflow time
// skipping enabled and returns a DevServerV2 whose Client stamps the given
// TSConfig on every workflow start.
//
// The dev server must include the WorkflowTimeSkippingEnabled dynamic config
// (add "--dynamic-config-value frontend.WorkflowTimeSkippingEnabled=true" to
// options.ExtraArgs). StartDevServerV2 does not add it automatically because
// callers may want their own dynamic-config bundles.
func StartDevServerV2(ctx context.Context, options DevServerV2Options) (*DevServerV2, error) {
	if !options.TSConfig.Enabled && options.TSConfig.FastForwardConfig != nil {
		return nil, errors.New("DevServerV2Options.TSConfig.FastForwardConfig cannot be set when Enabled is false")
	}
	ds, err := StartDevServer(ctx, options.DevServerOptions)
	if err != nil {
		return nil, err
	}
	namespace := "default"
	if options.ClientOptions != nil && options.ClientOptions.Namespace != "" {
		namespace = options.ClientOptions.Namespace
	}
	skipper, err := NewTimeSkipper(ds.Client(), namespace, options.TSConfig)
	if err != nil {
		_ = ds.Stop()
		return nil, fmt.Errorf("wrapping dev server with TimeSkipper: %w", err)
	}
	return &DevServerV2{DevServer: ds, skipper: skipper}, nil
}

// Client returns the stamping client. Every workflow started through it is
// stamped with the TimeSkippingConfig this DevServerV2 was created with,
// unless suspended via WithTimeSkippingDisabled.
func (d *DevServerV2) Client() client.Client { return d.skipper.Client() }

// TimeSkipper returns the underlying skipper, for direct access to its
// configuration setters if needed.
func (d *DevServerV2) TimeSkipper() *TimeSkipper { return d.skipper }

// FastForward is a convenience wrapper for d.TimeSkipper().FastForward.
// See TimeSkipper.FastForward for semantics.
func (d *DevServerV2) FastForward(
	ctx context.Context, run client.WorkflowRun, opts ...FastForwardOption,
) (bool, error) {
	return d.skipper.FastForward(ctx, run, opts...)
}

// GetTimeSkippingInfo returns the workflow's server-side TimeSkippingInfo,
// or nil if it has never had time skipping enabled.
func (d *DevServerV2) GetTimeSkippingInfo(
	ctx context.Context, run client.WorkflowRun,
) (*commonpb.TimeSkippingInfo, error) {
	return d.skipper.GetTimeSkippingInfo(ctx, run)
}

// GetCurrentTime returns the workflow's current virtual clock. Falls back to
// wall-clock time if the workflow has never had time skipping enabled.
func (d *DevServerV2) GetCurrentTime(
	ctx context.Context, run client.WorkflowRun,
) (time.Time, error) {
	return d.skipper.GetCurrentTime(ctx, run)
}

// WithTimeSkippingDisabled runs f with TimeSkippingConfig stamping suspended.
func (d *DevServerV2) WithTimeSkippingDisabled(f func()) {
	d.skipper.WithTimeSkippingDisabled(f)
}
