## [Unreleased]

## [0.1.2] - 2026-11-13

### Changed

- Internal refactor to build providers, resolve configuration, install the
  metrics handler and tracing interceptor, and flush through the shared
  `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` module. No public API
  change.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.

### Fixed

- Require `otlpworker v0.1.1` to prevent resource schema conflicts during
  provider construction with OpenTelemetry SDK v1.45.0.
