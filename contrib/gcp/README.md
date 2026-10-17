# Temporal Google Cloud module

Package `go.temporal.io/sdk/contrib/gcp` provides an OpenTelemetry plugin with defaults for Temporal Go SDK workers running on Google Cloud Run. Cloud Run worker pools are the recommended deployment because Temporal workers are continuous, pull-based background workloads.

> **Collector required by default:** The plugin exports metrics and traces to an OTLP collector at `http://localhost:4317`. It does not export directly to Google Cloud. Deploy the Google-Built OpenTelemetry Collector as a sidecar, configure another collector endpoint, or provide application-owned OpenTelemetry providers. Without a collector at the configured endpoint, telemetry is not delivered to Google Cloud.

This integration is for container-based Cloud Run workloads. It does not implement a Cloud Run functions invocation lifecycle.

A Cloud Run service can also host a Temporal worker, but it must use instance-based billing so CPU is available outside request handling, keep at least one instance active through minimum instances or manual scaling, and run an ingress container that listens on `PORT`. These are deployment requirements; the plugin cannot configure them from inside the worker process.

## Usage

Create the plugin and install it on the Temporal client. Client plugins that also implement `worker.Plugin` are automatically applied to workers created from that client.

```go
otelPlugin, err := gcp.NewOpenTelemetryPlugin(
	context.Background(),
	gcp.OpenTelemetryPluginOptions{},
)
if err != nil {
	return err
}

temporalClient, err := client.Dial(client.Options{
	Plugins: []client.Plugin{otelPlugin},
})
if err != nil {
	return err
}
worker := worker.New(temporalClient, taskQueue, worker.Options{})
```

The plugin configures the SDK metrics handler and tracing interceptor through `go.temporal.io/sdk/contrib/opentelemetry`. Do not install another OpenTelemetry metrics handler or tracing interceptor on the same client.

## Shutdown lifecycle

Stop every worker and close the Temporal client before shutting down the plugin. The following shape reserves two seconds for telemetry within Cloud Run's termination window:

```go
worker.Stop()
temporalClient.Close()

flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()
if err := otelPlugin.Shutdown(flushCtx); err != nil {
	logger.Error("failed to shut down OpenTelemetry", "error", err)
}
```

`Shutdown` flushes and closes metric and trace providers created by the plugin. Application-owned providers remain the application's responsibility and are force-flushed when they implement `ForceFlush(context.Context) error`; use `FlushHook` when they do not. Use `ForceFlush` instead of `Shutdown` when plugin-owned providers must remain usable.

`OpenTelemetryPluginOptions.FlushOnWorkerStop` enables immediate, best-effort flushing after an individual worker stops. It is disabled by default because one client can own multiple workers and the remaining workers can emit telemetry after the first worker stops.

The OTLP endpoint is resolved in this order:

1. `OpenTelemetryPluginOptions.Endpoint`.
2. `OTEL_EXPORTER_OTLP_ENDPOINT`.
3. `http://localhost:4317`.

Metrics are exported every 60 seconds by default, matching the OpenTelemetry SDK default. When
exporting cumulative metrics to Google Managed Service for Prometheus, do not put a collector
`batch` processor in the metrics pipeline. It can combine periodic and forced-shutdown snapshots
of the same time series into one Google Monitoring write, which is rejected as duplicate data.
Batching can remain enabled independently in the traces pipeline.

The OpenTelemetry service name is resolved in this order:

1. `OpenTelemetryPluginOptions.ServiceName`.
2. `OTEL_SERVICE_NAME`.
3. `CLOUD_RUN_WORKER_POOL` for a Cloud Run worker pool.
4. `K_SERVICE` for a Cloud Run service.
5. `temporal-worker`.

To use application-owned providers, set both `OpenTelemetryPluginOptions.MeterProvider` and `OpenTelemetryPluginOptions.TracerProvider`. In that path, the plugin does not create exporters or shut down either provider. `FlushHook` can override provider force-flushing.

The collector should use its GCP resource detector to add the Google Cloud attributes it recognizes. Do not rely on the detector to infer Cloud Run worker-pool-specific location or revision attributes; configure those explicitly with a collector resource processor if they are required. This module does not call the Google Cloud metadata server and adds no Google Cloud client libraries or exporters to the worker process.

## Collector sidecar

Google publishes the Google-Built OpenTelemetry Collector as a container image. Configure it as a second Cloud Run container, listen for OTLP gRPC on `localhost:4317`, and use its GCP exporters for metrics and traces. For the image, recommended collector configuration, IAM roles, health check, and Secret Manager mount, see [Deploy Google-Built OpenTelemetry Collector on Cloud Run](https://cloud.google.com/stackdriver/docs/instrumentation/opentelemetry-collector-cloud-run). That guide demonstrates a Cloud Run service; adapt its collector container and configuration when deploying a worker pool.

Use separate metrics and traces pipelines. Send cumulative metrics to the
`googlemanagedprometheus` exporter without a `batch` processor, and use a trace-specific batch
processor if desired. This also keeps `ForceFlush` during worker shutdown from colliding with a
still-open metrics batch.

Cloud Run worker pools support sidecar containers over localhost and are intended for continuous background work. The deployment should start the collector before the Temporal worker and use the collector health extension as its startup probe.

To use an external collector instead, set `OTEL_EXPORTER_OTLP_ENDPOINT` or `OpenTelemetryPluginOptions.Endpoint`.
