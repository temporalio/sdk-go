# Changelog

## [0.1.1] - 2026-11-25

### Fixed

- Use `otlpworker` v0.1.1 so the plugin can initialize its default OpenTelemetry providers without a resource schema conflict.

### Security

- Updated OpenTelemetry to v1.47.0 to fix an [attribute length limit bypass](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-q7gw-24fr-5p69) and [excessive memory allocation when parsing baggage](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-rrfc-gjm9-cgrj).

## [0.1.0] - 2026-09-29

### Added

- Initial release of the Google Cloud Run OpenTelemetry plugin. It configures
  the Temporal SDK metrics handler and tracing interceptor, creates OTLP gRPC
  providers targeting a localhost collector by default, resolves the service
  name from `OTEL_SERVICE_NAME`, `CLOUD_RUN_WORKER_POOL`, and `K_SERVICE`, and
  flushes telemetry on shutdown. Provider construction and flushing are delegated
  to the shared `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` module.

