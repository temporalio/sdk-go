package timeskipping

import (
	"context"

	"go.temporal.io/sdk/testsuite"
)

// StartDevServer starts a dev server with the dynamic config time skipping
// needs. What comes back is an ordinary [testsuite.DevServer]; the only thing
// added is server configuration. Nothing is applied to the workflows or
// schedules started through its client — enable time skipping per execution by
// passing a client.TimeSkippingConfig.
//
// The server defaults all of the following off, and without them time skipping
// is silently unavailable rather than loudly broken, so they are applied
// unconditionally rather than left to the caller to pass through ExtraArgs.
//
// They cannot simply be set on a shared dev server: forcing every schedule onto
// the CHASM v2 scheduler changes behaviour that existing tests depend on, which
// is why time skipping starts its own.
//
// Schedule time skipping is CHASM (v2) scheduler only, and new schedules are
// NOT created on CHASM by default — creation is off, behind a percentage
// rollout that also defaults to zero. Without all three of the history settings
// a schedule lands on the v1 workflow-backed scheduler and its time-skipping
// config is quietly ignored.
//
// The two feature flags use the names the server adopted after v1.33.0
// (main @ 0610a4f), with no aliases kept for the old ones:
//
//	frontend.WorkflowTimeSkippingEnabled      -> system.workflowTimeSkippingEnabled
//	history.enableCHASMSchedulerTimeSkipping  -> system.chasmSchedulerTimeSkippingEnabled
//
// These require a server at or past that rename. No published Temporal CLI
// release embeds one yet, so the pinned dev server
// (internal.PinnedDevServerVersion) does NOT satisfy this and the time-skipping
// suites fail against it — the server ignores dynamic-config keys it does not
// recognise, so time skipping is simply off. Supply a newer binary through
// options.ExistingPath until a CLI release catches up.
//
// NOTE: Experimental
func StartDevServer(
	ctx context.Context, options testsuite.DevServerOptions,
) (*testsuite.DevServer, error) {
	options.ExtraArgs = append(options.ExtraArgs,
		"--dynamic-config-value", "system.workflowTimeSkippingEnabled=true",
		"--dynamic-config-value", "system.chasmSchedulerTimeSkippingEnabled=true",
		"--dynamic-config-value", "history.enableCHASMSchedulerCreation=true",
		"--dynamic-config-value", "history.chasmSchedulerCreationRolloutPercent=100",
		"--dynamic-config-value", "history.enableCHASMSchedulerRouting=true",
	)
	return testsuite.StartDevServer(ctx, options)
}
