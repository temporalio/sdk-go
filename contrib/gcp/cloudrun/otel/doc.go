// Package otel provides an OpenTelemetry plugin with defaults for Temporal
// Go SDK workers running on Google Cloud Run.
//
// The plugin exports OTLP gRPC metrics and traces to a collector on localhost
// (by default http://localhost:4317); it does not export directly to Google
// Cloud and links no Google client libraries into the worker process. Deploy the
// Google-Built OpenTelemetry Collector as a Cloud Run sidecar, point the plugin
// at another collector, or supply application-owned OpenTelemetry providers.
//
// Install the plugin on the Temporal client; client plugins that also implement
// worker.Plugin are automatically applied to workers created from that client.
// After every worker and client has stopped, call
// [Plugin.Shutdown] to flush telemetry and release plugin-owned
// providers within Cloud Run's termination window.
//
// The provider-neutral OpenTelemetry glue used here lives in
// go.temporal.io/sdk/contrib/opentelemetry/otlpworker; this package layers the
// Cloud Run specific policy (service-name resolution from CLOUD_RUN_WORKER_POOL
// and K_SERVICE, a URL OTLP endpoint, a 60s default metric export interval) on
// top.
package otel
