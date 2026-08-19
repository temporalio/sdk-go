<!--
Release notes for go.temporal.io/sdk/contrib/gcp/cloudrun.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Initial release of the Google Cloud Run OpenTelemetry plugin. It configures
  the Temporal SDK metrics handler and tracing interceptor, creates OTLP gRPC
  providers targeting a localhost collector by default, resolves the service
  name from `OTEL_SERVICE_NAME`, `CLOUD_RUN_WORKER_POOL`, and `K_SERVICE`, and
  flushes telemetry on shutdown. Provider construction and flushing are delegated
  to the shared `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` module.
