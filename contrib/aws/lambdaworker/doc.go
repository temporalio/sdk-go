// Package lambdaworker provides an ergonomic wrapper for running
// Temporal workers inside AWS Lambda execution environments.
//
// # Usage
//
// Call [RunWorker] from your Lambda's main() function:
//
//	func main() {
//	    lambdaworker.RunWorker(worker.WorkerDeploymentVersion{
//	        DeploymentName: "my-service",
//	        BuildID:        "v1.0",
//	    }, func(ctx *lambdaworker.Options) error {
//	        ctx.TaskQueue = "my-task-queue"
//	        ctx.RegisterWorkflow(MyWorkflow)
//	        ctx.RegisterActivity(MyActivity)
//	        return nil
//	    })
//	}
//
// [RunWorker] handles the Lambda lifecycle: on each invocation it dials the Temporal server,
// creates and starts a worker with Lambda-appropriate defaults, polls for tasks until the
// invocation deadline approaches, and then gracefully shuts down the worker and closes the client.
// This per-invocation lifecycle ensures a clean worker state on every Lambda invocation.
//
// # Configuration
//
// Client connection options (address, namespace, TLS, API key) are loaded automatically from
// a TOML config file and environment variables via
// [go.temporal.io/sdk/contrib/envconfig.LoadClientOptions]. See more at
// https://docs.temporal.io/references/client-environment-configuration.
//
// Because AWS Lambda does not have a standard config file directory, it resolves the config file
// path in the following order:
//
//  1. TEMPORAL_CONFIG_FILE environment variable, if set.
//  2. temporal.toml in the Lambda code root ($LAMBDA_TASK_ROOT), which is typically /var/task.
//  3. temporal.toml in the current working directory as a final fallback.
//
// The file is optional — if it does not exist, only environment variables are used. See
// [go.temporal.io/sdk/contrib/envconfig] for the full list of supported environment variables
// and TOML fields.
//
// The configure callback receives an [Options] struct with public fields pre-populated with
// Lambda-appropriate defaults. Override any field directly in the callback.
//
// # Lambda Timeout
//
// The Lambda function timeout must be long enough for the worker to pick up a task, execute it,
// and shut down gracefully. Set it to at least the longest expected activity StartToClose timeout
// plus the worker stop timeout (default 7s). A minimum of 1 minute is recommended. If the timeout
// is too short, the worker may be terminated before it can complete in-progress tasks.
//
// # Observability
//
// Observability (metrics, tracing) is opt-in. Use [Options.MutateClientOptions] to
// supply your own metrics handler and tracing interceptor. A convenience sub-package at
// contrib/aws/lambdaworker/otel/ provides ready-made OTel configuration for AWS Distro
// for OpenTelemetry (ADOT).
package lambdaworker
