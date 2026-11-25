# Changelog

## [0.3.0] - 2026-11-24

### Changed

- **Experimental**: Store and retrieve operations now respect `ExternalStorage.Concurrency`.
- **Experimental**: Custom `BucketFunc` callbacks can now run concurrently for different payloads within a single `Store` call. Synchronize any shared state the callback accesses.
- **Experimental**: Payloads stored with no target are now written under `v0/ns/{namespace}/d/sha256/{hash}`, using the namespace of the client or worker (`null` when it is unknown), instead of `v0/d/sha256/{hash}`. Object names for payloads with a target are unchanged, and existing objects remain retrievable.

### :boom: Breaking Changes

- Raised the minimum Temporal Go SDK requirement from v1.49.0 to v1.52.0, which provides `StorageDriverStoreContext.Namespace`.

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
