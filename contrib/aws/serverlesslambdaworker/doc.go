// Package serverlesslambdaworker provides an ergonomic wrapper for running
// Temporal workers inside AWS Lambda execution environments.
//
// # Usage
//
// Call [RunWorker] from your Lambda's main() function:
//
//	func main() {
//	    serverlesslambdaworker.RunWorker(func(ctx *serverlesslambdaworker.ConfigureWorkerContext) error {
//	        ctx.SetTaskQueue("my-task-queue")
//	        ctx.RegisterWorkflow(MyWorkflow)
//	        ctx.RegisterActivity(MyActivity)
//	        return nil
//	    })
//	}
//
// [RunWorker] handles the Lambda lifecycle: it creates a Temporal client and worker with
// Lambda-appropriate defaults during the init phase, starts the worker on each invocation, and
// shuts it down gracefully before the Lambda deadline expires.
//
// # Configuration
//
// Client connection options (address, namespace, TLS, API key) are loaded automatically from
// a TOML config file and environment variables via
// [go.temporal.io/sdk/contrib/envconfig.LoadClientOptions].
//
// Because AWS Lambda does not set $HOME or $XDG_CONFIG_HOME, [RunWorker] does not use the
// standard user config directory for the config file. Instead, it resolves the config file path
// in the following order:
//
//  1. TEMPORAL_CONFIG_FILE environment variable, if set.
//  2. temporal.toml in the Lambda code root ($LAMBDA_TASK_ROOT), which is typically /var/task.
//  3. temporal.toml in the current working directory as a final fallback.
//
// The file is optional — if it does not exist, only environment variables are used. See
// [go.temporal.io/sdk/contrib/envconfig] for the full list of supported environment variables
// and TOML fields.
//
// Use [ConfigureWorkerContext.MutateClientOptions] and [ConfigureWorkerContext.MutateWorkerOptions]
// to override any defaults. User overrides are applied after Lambda defaults, so they always win.
//
// # Observability
//
// Observability (metrics, tracing) is opt-in. Use [ConfigureWorkerContext.MutateClientOptions] to
// supply your own metrics handler and tracing interceptor. A convenience sub-package at
// contrib/aws/serverlesslambdaworker/otel/ provides ready-made OTel configuration for AWS Distro
// for OpenTelemetry (ADOT).
package serverlesslambdaworker
