# OpenTelemetry for Temporal AWS Lambda workers

Package `go.temporal.io/sdk/contrib/aws/lambdaworker/otel` provides convenience
helpers for configuring OpenTelemetry metrics and tracing on Temporal workers
running in AWS Lambda. Its defaults are designed for the AWS Distro for
OpenTelemetry (ADOT) Lambda layer.

## Add to your project

Add both the Lambda worker and its OpenTelemetry helper from your application's
Go module:

```bash
go get go.temporal.io/sdk/contrib/aws/lambdaworker@latest
go get go.temporal.io/sdk/contrib/aws/lambdaworker/otel@latest
```

## Module versioning

The OpenTelemetry helper is released separately from the core Temporal Go SDK
and `lambdaworker`. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Call `ApplyDefaults` from the `lambdaworker.RunWorker` configuration callback:

```go
lambdaworker.RunWorker(version, func(options *lambdaworker.Options) error {
	options.TaskQueue = "my-task-queue"
	if err := otel.ApplyDefaults(
		options,
		&options.ClientOptions,
		otel.Options{},
	); err != nil {
		return err
	}
	options.RegisterWorkflow(MyWorkflow)
	options.RegisterActivity(MyActivity)
	return nil
})
```

`ApplyDefaults` configures OTLP gRPC exporters, AWS X-Ray-compatible trace IDs,
and per-invocation flushing. Use `ApplyDefaultsWithProviders`, `ApplyMetrics`,
or `ApplyTracing` when you need more control over provider configuration.
