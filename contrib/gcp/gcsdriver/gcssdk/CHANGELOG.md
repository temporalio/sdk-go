# Changelog

## [0.3.0] - 2026-11-24

### :boom: Breaking Changes

- Raised the minimum Temporal Go SDK requirement from v1.49.0 to v1.50.0 to match the `gcsdriver` module.

## [0.2.0] - 2026-10-08

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Raised the minimum Temporal Go SDK requirement from v1.43.1 to v1.49.0, which
  passes the driver selector a dedicated select context instead of the store
  context.

## [0.1.0] - 2026-08-13

### Added

- Initial release of the GCS SDK client implementation backed by
  `cloud.google.com/go/storage`.
