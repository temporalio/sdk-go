# Tally metrics for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/tally` adapts a
[`tally.Scope`](https://pkg.go.dev/github.com/uber-go/tally/v4#Scope) to the
Temporal SDK's `client.MetricsHandler` interface.

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/tally@latest
```

## Module versioning

The Tally integration is released as a separate Go module from the core
Temporal Go SDK. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Create and configure a Tally scope, then attach it to your client options:

```go
options := client.Options{
	MetricsHandler: temporaltally.NewMetricsHandler(scope),
}

c, err := client.Dial(options)
```

For Prometheus-compatible metric names, use `PrometheusSanitizeOptions` and
wrap the scope with `NewPrometheusNamingScope`. See the [package documentation](https://pkg.go.dev/go.temporal.io/sdk/contrib/tally)
for the complete setup.
