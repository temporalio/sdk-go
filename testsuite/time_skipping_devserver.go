package testsuite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
)

// timeSkippingDynamicConfig are the dynamic-config values a dev server needs
// for per-workflow time skipping. The server defaults this off, and without it
// time skipping is silently unavailable rather than loudly broken.
//
// Values are parsed as JSON by the CLI, so non-scalar settings need JSON
// encoding (e.g. a string list as `["x"]`).
var timeSkippingDynamicConfig = []string{
	"frontend.WorkflowTimeSkippingEnabled=true",
}

// DevServerV2Options configures a v2 time-skipping dev server. Same shape as
// DevServerOptions plus an initial TimeSkippingConfig stamped on every
// workflow started through the returned server's Client.
//
// NOTE: Experimental
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
// NOTE: Experimental
type DevServerV2 struct {
	*DevServer
	skipper *TimeSkipper
}

// StartDevServerV2 starts a Temporal dev server with per-workflow time
// skipping enabled and returns a DevServerV2 whose Client stamps the given
// TSConfig on every workflow start.
//
// The dynamic-config values time skipping requires are added automatically.
// A caller that sets one of those keys in options.ExtraArgs keeps its own
// value.
//
// NOTE: Experimental
func StartDevServerV2(ctx context.Context, options DevServerV2Options) (*DevServerV2, error) {
	if !options.TSConfig.Enabled && options.TSConfig.FastForwardConfig != nil {
		return nil, errors.New("DevServerV2Options.TSConfig.FastForwardConfig cannot be set when Enabled is false")
	}
	options.ExtraArgs = withDynamicConfig(options.ExtraArgs, timeSkippingDynamicConfig)
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

// withDynamicConfig appends each "key=value" as a --dynamic-config-value pair,
// skipping any key the caller already set in extraArgs.
func withDynamicConfig(extraArgs []string, values []string) []string {
	for _, kv := range values {
		key, _, ok := strings.Cut(kv, "=")
		if !ok || dynamicConfigHasKey(extraArgs, key) {
			continue
		}
		extraArgs = append(extraArgs, "--dynamic-config-value", kv)
	}
	return extraArgs
}

func dynamicConfigHasKey(extraArgs []string, key string) bool {
	for _, arg := range extraArgs {
		if existing, _, ok := strings.Cut(arg, "="); ok && existing == key {
			return true
		}
	}
	return false
}

// Client returns the stamping client. Every workflow started through it is
// stamped with the TimeSkippingConfig this DevServerV2 was created with,
// unless suspended via WithTimeSkippingDisabled.
//
// NOTE: Experimental
func (d *DevServerV2) Client() client.Client { return d.skipper.Client() }

// TimeSkipper returns the underlying skipper, for direct access to its
// configuration setters if needed.
//
// NOTE: Experimental
func (d *DevServerV2) TimeSkipper() *TimeSkipper { return d.skipper }

// FastForward is a convenience wrapper for d.TimeSkipper().FastForward.
// See TimeSkipper.FastForward for semantics.
//
// NOTE: Experimental
func (d *DevServerV2) FastForward(
	ctx context.Context, run client.WorkflowRun, opts ...FastForwardOption,
) (bool, error) {
	return d.skipper.FastForward(ctx, run, opts...)
}

// GetTimeSkippingInfo returns the workflow's server-side TimeSkippingInfo,
// or nil if it has never had time skipping enabled.
//
// NOTE: Experimental
func (d *DevServerV2) GetTimeSkippingInfo(
	ctx context.Context, run client.WorkflowRun,
) (*commonpb.TimeSkippingInfo, error) {
	return d.skipper.GetTimeSkippingInfo(ctx, run)
}

// GetCurrentTime returns the workflow's current virtual clock. Falls back to
// wall-clock time if the workflow has never had time skipping enabled.
//
// NOTE: Experimental
func (d *DevServerV2) GetCurrentTime(
	ctx context.Context, run client.WorkflowRun,
) (time.Time, error) {
	return d.skipper.GetCurrentTime(ctx, run)
}

// WithTimeSkippingDisabled runs f with TimeSkippingConfig stamping suspended.
//
// NOTE: Experimental
func (d *DevServerV2) WithTimeSkippingDisabled(f func()) {
	d.skipper.WithTimeSkippingDisabled(f)
}
