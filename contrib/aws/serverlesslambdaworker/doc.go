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
// [RunWorker] handles the Lambda lifecycle: it creates a Temporal client and
// worker with Lambda-appropriate defaults during the init phase, starts the
// worker on each invocation, and shuts it down gracefully before the Lambda
// deadline expires.
//
// Client connection options (address, namespace, TLS, API key) are loaded
// automatically from environment variables and config files via
// [go.temporal.io/sdk/contrib/envconfig.LoadDefaultClientOptions].
//
// Use [ConfigureWorkerContext.MutateClientOptions] and
// [ConfigureWorkerContext.MutateWorkerOptions] to override any defaults.
// User overrides are applied after Lambda defaults, so they always win.
//
// # Observability
//
// Observability (metrics, tracing) is opt-in. Use [ConfigureWorkerContext.MutateClientOptions]
// to supply your own metrics handler and tracing interceptor. A convenience
// sub-package at contrib/aws/serverlesslambdaworker/otel/ provides ready-made
// OTel configuration for AWS Distro for OpenTelemetry (ADOT).
package serverlesslambdaworker
