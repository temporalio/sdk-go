# Changelog

## [0.7.0] - 2026-11-16

### Changed

- Updated indirect OpenTelemetry API dependencies from v1.44.0 to v1.47.0.

### Security

- Updated `github.com/klauspost/compress` from v1.18.4 to v1.18.7, including
  the upstream fix for an out-of-bounds read in `s2.NewDict`.

### Added

- `tracing.TracerOptions.EncodeHeaders` opts tracing headers into processing using the configured
  client's payload codec. Defaults to false; application-supplied headers are unchanged.

### Fixed

- `DisableUpdateTracing` now disables Datadog update spans.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Raised the minimum Temporal Go SDK requirement to v1.51.0.

## [0.6.0] - 2026-07-16

### Changed

- Increased the module's minimum required Go version from 1.24 to 1.25.4.

### Security

- Upgraded `github.com/DataDog/dd-trace-go/v2` from v2.4.0 to v2.8.1 to address a potential
  denial of service when extracting W3C baggage headers.
