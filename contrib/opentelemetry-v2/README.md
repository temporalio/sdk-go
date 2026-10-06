# OpenTelemetry v2 integration for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/opentelemetry-v2` provides OpenTelemetry
tracing and metrics for Temporal. Experimental: APIs may change between releases.

## Usage

```bash
go get go.temporal.io/sdk/contrib/opentelemetry-v2@latest
```

Create a tracer provider, install it as the OpenTelemetry global, and register
the plugin on the client (examples import this module as `temporalotel`):

```go
import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry-v2"
	"go.temporal.io/sdk/worker"
)

ctx := context.Background()
exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
if err != nil {
	return err
}

// Create a replay-safe tracer provider and set it as the OpenTelemetry global
provider := temporalotel.NewReplaySafeTracerProvider(sdktrace.WithBatcher(exporter))
otel.SetTracerProvider(provider)
defer provider.Shutdown(ctx)

// Register the plugin on the client
plugin, err := temporalotel.NewPlugin(temporalotel.PluginOptions{})
if err != nil {
	return err
}
c, err := client.Dial(client.Options{Plugins: []client.Plugin{plugin}})
if err != nil {
	return err
}

// Workers created from this client automatically get the plugin
w := worker.New(c, "my-task-queue", worker.Options{})
```

## Temporal spans

By default the plugin creates no spans of its own. It propagates trace context
through Temporal headers, so spans your own code creates stay connected across
clients, workflows, activities, and Nexus operations.

Set `PluginOptions.TracerOptions.AddTemporalSpans` to also emit a span per
Temporal operation, such as `StartWorkflow`, `RunWorkflow`, `RunActivity`, and
`ContinueAsNew`.

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

`NewReplaySafeTracerProvider` keeps sequenced workflow span IDs stable and their
start times accurate across retries and replays.

## Metrics

Matches v1. Set `PluginOptions.MetricsHandlerOptions` to have the plugin install
a metrics handler.
