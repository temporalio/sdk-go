# lambdaworker

A wrapper for running [Temporal](https://temporal.io) workers inside AWS Lambda. A single
`RunWorker` call handles the full per-invocation lifecycle: dialing the Temporal server, creating a
worker with Lambda-tuned defaults, polling for tasks, and gracefully shutting down before the
invocation deadline.

## Quick start

```go
package main

import "go.temporal.io/sdk/contrib/aws/lambdaworker"

func main() {
    lambdaworker.RunWorker(
        func(ctx *lambdaworker.ConfigureWorkerContext) error {
            ctx.SetTaskQueue("my-task-queue")
            ctx.RegisterWorkflow(MyWorkflow)
            ctx.RegisterActivity(MyActivity)
            return nil
        },
    )
}
```

## Configuration

Client connection settings (address, namespace, TLS, API key) are loaded
automatically from a TOML config file and/or environment variables via
`go.temporal.io/sdk/contrib/envconfig`. The config file is resolved in order:

1. `TEMPORAL_CONFIG_FILE` env var, if set.
2. `temporal.toml` in `$LAMBDA_TASK_ROOT` (typically `/var/task`).
3. `temporal.toml` in the current working directory.

The file is optional — if absent, only environment variables are used.

Use `MutateClientOptions` and `MutateWorkerOptions` on the
`ConfigureWorkerContext` to override any defaults programmatically. User
overrides are applied last, so they always win.

## Lambda-tuned worker defaults

The package applies conservative concurrency limits suited to Lambda's resource constraints, see the
docstrings for more. Eager activities are always disabled.

## Observability

Metrics and tracing are opt-in. The `otel` sub-package provides convenience
helpers for AWS Distro for OpenTelemetry (ADOT):

```go
import (
    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/contrib/aws/lambdaworker/otel"
)

ctx.MutateClientOptions(func(opts *client.Options) error {
    return otel.ApplyDefaults(ctx, opts, otel.Options{})
})
```

You can also use `otel.ApplyMetrics` or `otel.ApplyTracing` individually.

If you use OTEL, you can use
[ADOT](https://aws-otel.github.io/docs/getting-started/lambda/lambda-go) (the AWS Distro For
OpenTelemetry) to automatically integrate with AWS observability functionality. Namely, you will
want to add the Lambda layer in the aforementioned link. We'll handle setting up the SDK for you.

See more [here](https://aws-observability.github.io/aws-otel-collector/)
