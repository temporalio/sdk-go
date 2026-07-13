# OpenTelemetry integration for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/opentelemetry` provides Temporal tracing and
metrics integrations backed by OpenTelemetry.

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/opentelemetry@latest
```

## Releases

The OpenTelemetry integration is released as a separate Go module from the core
Temporal Go SDK. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Configure your OpenTelemetry providers and exporters, then attach the Temporal
metrics handler and tracing interceptor to your client options:

```go
tracingInterceptor, err := temporalotel.NewTracingInterceptor(
	temporalotel.TracerOptions{},
)
if err != nil {
	return err
}

options := client.Options{
	MetricsHandler: temporalotel.NewMetricsHandler(
		temporalotel.MetricsHandlerOptions{},
	),
}
options.Interceptors = append(options.Interceptors, tracingInterceptor)

c, err := client.Dial(options)
```

The zero-value options use the global OpenTelemetry providers. See the [package documentation](https://pkg.go.dev/go.temporal.io/sdk/contrib/opentelemetry)
to supply explicit providers and configure propagation.
