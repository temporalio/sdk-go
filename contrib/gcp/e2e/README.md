# Google Cloud OpenTelemetry end-to-end harness

This command exercises the module's default localhost OTLP exporters against a real Temporal
namespace. The worker mode is intended for a Cloud Run worker pool with a Google-Built
OpenTelemetry Collector sidecar on port 4317. The starter mode can run from a trusted local or CI
environment.

Both modes require `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE`, and `TEMPORAL_TASK_QUEUE`. Supply the
Temporal API key through `TEMPORAL_API_KEY` or `TEMPORAL_API_KEY_FILE`; surrounding whitespace is
trimmed and the value is never logged. Set `E2E_MODE=starter` to start one workflow and verify its
result. Worker mode is the default.

The worker waits for the collector before connecting to Temporal. On `SIGINT` or `SIGTERM`, it
stops the worker, closes the Temporal client, force-flushes the plugin, and shuts the plugin down.
Set `E2E_SHUTDOWN_AFTER` to a positive Go duration, such as `30s`, to exercise that lifecycle while
the collector sidecar remains healthy. A worker pool may restart the process after this intentional
exit, so use this only for a controlled E2E and scale the pool to zero afterward.
