# Changelog

## [0.2.0] - 2026-10-08

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Raised the minimum Temporal Go SDK requirement from v1.43.1 to v1.49.0, which
  passes the driver selector a dedicated select context instead of the store
  context.

### Changed

- Claim metadata now identifies the stored object with `object_name`. The legacy
  `key` field is still written and still read, so claims resolve in both
  directions while a deployment runs mixed driver versions. Support for `key`
  will be removed when the driver reaches GA.
- Store and retrieve error messages now report `object_name=` instead of `key=`.

## [0.1.0] - 2026-08-13

### Added

- Initial release of the GCS storage driver for external payload storage,
  mirroring the existing S3 driver architecture (`contrib/aws/s3driver`).
