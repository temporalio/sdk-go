# Datadog tracing for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/datadog/tracing` provides a Temporal tracing
interceptor backed by [Datadog's Go tracer](https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/ddtrace/tracer).

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/datadog/tracing@latest
```

## Module versioning

The Datadog integration is released as a separate Go module from the core
Temporal Go SDK. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Configure and start the Datadog tracer, then add the Temporal tracing
interceptor to your client options:

```go
options := client.Options{}
options.Interceptors = append(options.Interceptors,
	tracing.NewTracingInterceptor(tracing.TracerOptions{}))

c, err := client.Dial(options)
```

See the [`tracing` package documentation](https://pkg.go.dev/go.temporal.io/sdk/contrib/datadog/tracing)
for configuration options and workflow span access.
