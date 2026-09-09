## [Unreleased]

### Changed

- Internal refactor to build providers, resolve configuration, install the
  metrics handler and tracing interceptor, and flush through the shared
  `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` module. No public API
  change.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
