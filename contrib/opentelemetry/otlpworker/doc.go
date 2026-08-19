// Package otlpworker contains provider-neutral OpenTelemetry glue shared by
// Temporal serverless-worker integrations that export OTLP telemetry to a local
// collector (for example AWS Lambda and Google Cloud Run).
//
// It is intentionally cloud-agnostic: it carries no cloud-provider policy such
// as X-Ray trace IDs, AWS Lambda function-name resolution, or Cloud Run service
// resolution. Higher-level packages such as
// go.temporal.io/sdk/contrib/aws/lambdaworker/otel and
// go.temporal.io/sdk/contrib/gcp/cloudrun layer that policy on top of the
// helpers here.
//
// The building blocks are:
//
//   - [NewProviders] constructs OTLP gRPC metric and trace providers that share
//     a resource carrying service.name. Metrics use a [PeriodicReader] with no
//     batch processor; traces use a batch span processor.
//   - [Apply], [ApplyMetrics] and [ApplyTracing] install the
//     go.temporal.io/sdk/contrib/opentelemetry metrics handler and tracing
//     interceptor on client options.
//   - [FirstNonEmptyEnv] resolves a value from an explicit setting, an ordered
//     list of environment variables, then a fallback.
//   - [ForceFlush] and [Shutdown] fan out to providers concurrently, joining any
//     errors.
package otlpworker
