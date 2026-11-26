# Changelog

## [0.4.0] - 2026-11-16

### Added

- `TracerOptions.EncodeHeaders` opts tracing headers into codec processing using the configured
  client's payload codec. Defaults to false; application-supplied headers are unchanged.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Raised the minimum Temporal Go SDK requirement to v1.51.0.

