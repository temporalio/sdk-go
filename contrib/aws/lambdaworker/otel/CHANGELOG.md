# Changelog

## [0.2.0] - 2026-11-24

### Changed

- Internal refactor to build providers, resolve configuration, install the metrics handler and tracing interceptor, and flush through the shared `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` module. No public API change.

### :boom: Breaking Changes

- Raised the minimum supported Go version to 1.26.0.

### Security

- Updated OpenTelemetry to v1.47.0 through `otlpworker` to fix an [attribute length limit bypass](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-q7gw-24fr-5p69) and [excessive memory allocation when parsing baggage](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-rrfc-gjm9-cgrj).

