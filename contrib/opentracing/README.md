# OpenTracing integration for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/opentracing` provides a Temporal tracing
interceptor backed by OpenTracing.

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/opentracing@latest
```

## Releases

The OpenTracing integration is released as a separate Go module from the core
Temporal Go SDK. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Configure an OpenTracing tracer, then add the Temporal tracing interceptor to
your client options:

```go
tracingInterceptor, err := temporalopentracing.NewInterceptor(
	temporalopentracing.TracerOptions{},
)
if err != nil {
	return err
}

options := client.Options{}
options.Interceptors = append(options.Interceptors, tracingInterceptor)

c, err := client.Dial(options)
```

The zero-value options use the global OpenTracing tracer. See the [package documentation](https://pkg.go.dev/go.temporal.io/sdk/contrib/opentracing)
for configuration options.
