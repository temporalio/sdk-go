# OpenTelemetry v2 integration for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/opentelemetry-v2` provides OpenTelemetry
tracing and metrics for Temporal. Experimental: APIs may change between releases.

For the stable integration, use
[`contrib/opentelemetry`](../opentelemetry/README.md) (v1).

> ⚠️ v1 and v2 use different span parenting and kinds; mixing them in the same
> workflows breaks traces.

## Usage

```bash
go get go.temporal.io/sdk/contrib/opentelemetry-v2@latest
```

Create a tracer provider, install it as the OpenTelemetry global, and register
the plugin on the client (examples import this module as `temporalotel`):

```go
import (
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry-v2"
)

provider := temporalotel.NewTracerProvider(sdktrace.WithBatcher(exporter))
otel.SetTracerProvider(provider)
defer provider.Shutdown(ctx) // after clients and workers stop

plugin, err := temporalotel.NewPlugin(temporalotel.PluginOptions{})
if err != nil {
	return err
}

c, err := client.Dial(client.Options{Plugins: []client.Plugin{plugin}})
```

Workers from this client are configured automatically.

## User spans in workflow code

Construct a tracer with `Tracer` and pass the `workflow.Context` to `Start`:

```go
func MyWorkflow(ctx workflow.Context) error {
	tracer := temporalotel.Tracer("my-workflows")
	sctx, span := tracer.Start(ctx, "my-span")
	defer span.End()
	return workflow.ExecuteActivity(sctx, MyActivity).Get(sctx, nil)
}
```

In client and activity code, use `otel.Tracer(...)` directly.

## Stable span IDs and accurate timing

`NewTracerProvider` keeps sequenced workflow span IDs stable and their start
times accurate across retries and replays.

## Metrics

Matches v1. Set `PluginOptions.MetricsHandlerOptions` to have the plugin install
a metrics handler.
