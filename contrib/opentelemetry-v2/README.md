# OpenTelemetry v2 integration for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/opentelemetry-v2` provides Temporal tracing
and metrics integrations backed by OpenTelemetry. This module is experimental:
APIs may change in incompatible ways between releases.

The "v2" in the module name refers to this second generation of the Temporal
OpenTelemetry integration — not a Go major version (the module is versioned
v0.x like the other contrib modules) and not the OpenTelemetry API version (it
uses the same OpenTelemetry Go dependency as v1). For the stable integration,
use [`go.temporal.io/sdk/contrib/opentelemetry`](../opentelemetry/README.md)
(v1).

Compared to v1, this module offers:

- Corrected span parenting and span kinds (outbound spans are `client`, inbound
  spans are `server`)
- Stable span and trace IDs across workflow retries and replays
- Plugin-based setup that configures the client and its workers in one place

> ⚠️ **Compatibility warning**: v1 and v2 produce spans with different parenting
> and span-kind semantics. Mixing them across clients and workers in the same
> workflows produces broken traces. Pick one version per fleet and migrate
> atomically, or accept a break in trace continuity during rollout.

## Add to your project

```bash
go get go.temporal.io/sdk/contrib/opentelemetry-v2@latest
```

## Module versioning

This integration is a separate Go module from the core Temporal Go SDK. See
[CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Use the plugin to configure tracing on the client and on workers created from
that client (the examples import this module as `temporalotel`):

```go
import temporalotel "go.temporal.io/sdk/contrib/opentelemetry-v2"

plugin, shutdown, err := temporalotel.NewPlugin(temporalotel.PluginOptions{
	ProviderOptions: []sdktrace.TracerProviderOption{sdktrace.WithBatcher(exporter)},
})
if err != nil {
	return err
}
defer shutdown(ctx)

c, err := client.Dial(client.Options{
	Plugins: []client.Plugin{plugin},
})
```

The plugin builds and owns its tracer provider from `ProviderOptions` (pass your
exporters and resources there). Workers created from this client are configured
automatically. Call the returned `shutdown` function on process exit to flush
and shut down the provider. Propagation defaults to `DefaultTextMapPropagator`
(W3C Trace Context + Baggage); set
`PluginOptions.TracerOptions.TextMapPropagator` to override.

### Deterministic span IDs

The plugin enables this for you. Workflow spans get deterministic IDs so they
stay stable across retries and replays; client and activity spans get random
IDs. No extra configuration is required.

### User spans in workflow code

In client and activity code, use the OpenTelemetry SDK directly. For workflow
code, create a deterministic provider with `NewTracerProvider` and make
it available to the workflow implementation:

```go
workflowProvider := temporalotel.NewTracerProvider(
	sdktrace.WithBatcher(exporter),
)
defer workflowProvider.Shutdown(ctx)

type Workflows struct {
	tracerProvider trace.TracerProvider
}

func (w *Workflows) MyWorkflow(ctx workflow.Context) error {
	tracer := temporalotel.NewTracer(w.tracerProvider, "my-workflows")
	sctx, span := tracer.Start(ctx, "my-span")
	defer span.End()
	return workflow.ExecuteActivity(sctx, MyActivity).Get(sctx, nil)
}
```

You can pass OpenTelemetry `trace.SpanStartOption` values after the name (for
example `trace.WithAttributes(...)`). Spans parent under the current Temporal
span and keep stable IDs across retries and replays.

Multiple workflow tracers on the same workflow share a single application-span
counter on the workflow context automatically, so their spans get distinct
deterministic IDs and never collide.

For spans started outside the workflow's event stream — query handlers and
update validators, which may run any number of times — call `StartUnsequenced`
on the same tracer. It carries the parent span through the `workflow.Context`
but does not consume the deterministic id stream; the provider falls back to a
random id instead.

```go
func (w *Workflows) MyWorkflow(ctx workflow.Context) error {
	tracer := temporalotel.NewTracer(w.tracerProvider, "my-workflows")
	sctx, span := tracer.Start(ctx, "my-span")
	defer span.End()

	return workflow.SetQueryHandler(sctx, "myQuery", func() (string, error) {
		_, qspan := tracer.StartUnsequenced(sctx, "my-query-span")
		defer qspan.End()
		return "ok", nil
	})
}
```

## Metrics

The metrics handler matches v1. The plugin installs it only when
`PluginOptions.MetricsHandlerOptions` is set; by default the plugin configures
tracing only and leaves any user-set metrics handler untouched.

## Migrating from v1

- Change the import path from `go.temporal.io/sdk/contrib/opentelemetry` to
  `go.temporal.io/sdk/contrib/opentelemetry-v2`.
- Use `NewPlugin` to configure tracing and metrics.
