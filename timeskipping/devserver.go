package timeskipping

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

// dynamicConfig are the dynamic-config values a dev server needs for time
// skipping. The server defaults all of them off, and without them time skipping
// is silently unavailable rather than loudly broken.
//
// These cannot simply be set on a shared dev server: forcing every schedule onto
// the CHASM v2 scheduler changes behaviour that existing tests depend on, which
// is why time skipping starts its own.
//
// The CLI parses values as JSON, so a non-scalar setting needs JSON encoding —
// a string list is ["x"], not x. A bare value makes the server refuse to start.
// That applies to anything a caller adds through ExtraArgs too.
var dynamicConfig = []string{
	"frontend.WorkflowTimeSkippingEnabled=true",

	// Schedule time skipping is CHASM (v2) scheduler only, and new schedules are
	// NOT created on CHASM by default — creation is off, behind a percentage
	// rollout that also defaults to zero. Without all three of these a schedule
	// lands on the v1 workflow-backed scheduler and its time-skipping config is
	// quietly ignored.
	"history.enableCHASMSchedulerTimeSkipping=true",
	"history.enableCHASMSchedulerCreation=true",
	"history.chasmSchedulerCreationRolloutPercent=100",
}

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
	options.ExtraArgs = withDynamicConfig(options.ExtraArgs, dynamicConfig)
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

// dynamicConfigHasKey reports whether extraArgs already sets a dynamic-config
// key. It understands both spellings the CLI accepts, and only inspects
// arguments that are actually dynamic-config values — cutting on "=" across
// every argument would read "--log-format=json" as the key "--log-format".
func dynamicConfigHasKey(extraArgs []string, key string) bool {
	for i, arg := range extraArgs {
		// Separate form: --dynamic-config-value k=v
		if arg == "--dynamic-config-value" && i+1 < len(extraArgs) {
			if k, _, ok := strings.Cut(extraArgs[i+1], "="); ok && k == key {
				return true
			}
		}
		// Joined form: --dynamic-config-value=k=v
		if rest, ok := strings.CutPrefix(arg, "--dynamic-config-value="); ok {
			if k, _, ok := strings.Cut(rest, "="); ok && k == key {
				return true
			}
		}
	}
	return false
}
