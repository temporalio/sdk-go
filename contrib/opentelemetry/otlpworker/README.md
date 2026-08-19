# OTLP worker OpenTelemetry glue

Package `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` contains the
provider-neutral OpenTelemetry building blocks shared by Temporal
serverless-worker integrations that export OTLP telemetry to a local collector,
such as AWS Lambda (`go.temporal.io/sdk/contrib/aws/lambdaworker/otel`) and
Google Cloud Run (`go.temporal.io/sdk/contrib/gcp/cloudrun`).

It is deliberately cloud-agnostic. It carries no cloud-provider policy (no X-Ray
trace IDs, no Lambda function-name resolution, no Cloud Run service resolution)
and it lives in its own module so the base `go.temporal.io/sdk/contrib/opentelemetry`
package remains free of exporter dependencies. Most applications should use one
of the cloud-specific packages above rather than this one directly.

## What it provides

- `NewProviders(ctx, Config)` builds an OTLP gRPC metric provider and trace
  provider that share a resource carrying `service.name`. Metrics use a
  `PeriodicReader` (no batch processor); traces use a batch span processor. If
  the trace exporter cannot be created, the meter provider is shut down before
  returning.
- `Apply`, `ApplyMetrics`, `ApplyTracing` install the
  `go.temporal.io/sdk/contrib/opentelemetry` metrics handler and tracing
  interceptor on client options.
- `FirstNonEmptyEnv` resolves a value from an explicit setting, an ordered list
  of environment variables (whitespace-only values are ignored), then a
  fallback.
- `ForceFlush` and `Shutdown` fan out to providers concurrently and join errors.
  Providers that do not implement the relevant method are ignored.

## Endpoint modes

`Config.EndpointMode` selects how `Config.Endpoint` is interpreted:

- `EndpointURL` (default) passes the endpoint to `WithEndpointURL`; the URL
  scheme selects transport security.
- `EndpointHostPort` passes a `host:port` to `WithEndpoint`; use `Config.Insecure`
  for a plaintext connection. An empty endpoint leaves the exporter default in
  place.

## Module versioning

This module is released separately from the core Temporal Go SDK. See
[CHANGELOG.md](CHANGELOG.md) for release notes.
