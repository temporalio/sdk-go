# Changelog

## [0.4.0] - 2026-11-24

### Changed

- **Experimental**: Store and retrieve operations now respect `ExternalStorage.Concurrency`.
- **Experimental**: Payloads stored with no target are now written under `v0/ns/{namespace}/d/sha256/{hash}`, using the namespace of the client or worker (`null` when it is unknown), instead of `v0/d/sha256/{hash}`. Object keys for payloads with a target are unchanged, and existing objects remain retrievable.

### :boom: Breaking Changes

- Raised the minimum Temporal Go SDK requirement from v1.49.0 to v1.50.0, which provides the `ExternalStorage.Concurrency` limiter the driver now uses.

## [0.3.0] - 2026-10-08

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Raised the minimum Temporal Go SDK requirement from v1.43.1 to v1.49.0, which
  passes the driver selector a dedicated select context instead of the store
  context.

## [0.2.1] - 2026-08-13

### Fixed

- Corrected the module's minimum Temporal Go SDK requirement from v1.25.1 to
  v1.43.1.

### Changed

- S3 object key path segments are now percent-encoded against S3's safe
  character set (alphanumerics and `!-_.*'()`), encoding all other bytes of
  their UTF-8 representation as `%XX`.
