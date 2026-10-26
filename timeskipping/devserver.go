package timeskipping

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

// DevServerOptions configures a time-skipping dev server. Same shape as
// testsuite.DevServerOptions, plus the configuration stamped on every workflow
// started through the returned server's Client.
//
// NOTE: Experimental
type DevServerOptions struct {
	testsuite.DevServerOptions

	// Config is stamped on every workflow started through DevServer.Client. The
	// zero value means Enabled=true with no bounded fast-forward.
	Config TimeSkippingConfig
}

// DevServer is a dev-server process with time skipping enabled. It embeds the
// underlying testsuite.DevServer and a TimeSkipper, so the fast-forward and
// virtual-clock operations are available directly on it.
//
// NOTE: Experimental
type DevServer struct {
	*testsuite.DevServer
	*TimeSkipper
}

func startDevServer(ctx context.Context, options DevServerOptions) (*DevServer, error) {
	// The dynamic config time skipping needs. The server defaults all of it off,
	// and without it time skipping is silently unavailable rather than loudly
	// broken, so these are applied unconditionally rather than left to the caller
	// to override through ExtraArgs.
	//
	// They cannot simply be set on a shared dev server: forcing every schedule
	// onto the CHASM v2 scheduler changes behaviour that existing tests depend
	// on, which is why time skipping starts its own.
	//
	// Schedule time skipping is CHASM (v2) scheduler only, and new schedules are
	// NOT created on CHASM by default — creation is off, behind a percentage
	// rollout that also defaults to zero. Without all three of the history
	// settings a schedule lands on the v1 workflow-backed scheduler and its
	// time-skipping config is quietly ignored.
	options.ExtraArgs = append(options.ExtraArgs,
		"--dynamic-config-value", "frontend.WorkflowTimeSkippingEnabled=true",
		"--dynamic-config-value", "history.enableCHASMSchedulerTimeSkipping=true",
		"--dynamic-config-value", "history.enableCHASMSchedulerCreation=true",
		"--dynamic-config-value", "history.chasmSchedulerCreationRolloutPercent=100",
	)
	ds, err := testsuite.StartDevServer(ctx, options.DevServerOptions)
	if err != nil {
		return nil, err
	}
	namespace := "default"
	if options.ClientOptions != nil && options.ClientOptions.Namespace != "" {
		namespace = options.ClientOptions.Namespace
	}
	// NewTimeSkipper validates the config; no need to pre-check it here.
	skipper, err := NewTimeSkipper(ds.Client(), namespace, options.Config)
	if err != nil {
		_ = ds.Stop()
		return nil, fmt.Errorf("wrapping dev server with TimeSkipper: %w", err)
	}
	return &DevServer{DevServer: ds, TimeSkipper: skipper}, nil
}

// Client returns the stamping client. Every workflow started through it carries
// the Config this DevServer was created with, unless suspended via
// WithTimeSkippingDisabled.
//
// This overrides the embedded testsuite.DevServer.Client, which returns the
// unwrapped client. The other TimeSkipper operations are promoted as-is.
//
// NOTE: Experimental
func (d *DevServer) Client() client.Client { return d.TimeSkipper.Client() }
