package timeskipping

import (
	"context"

	"go.temporal.io/sdk/testsuite"
)

// StartDevServer starts a dev server with time skipping enabled.
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
