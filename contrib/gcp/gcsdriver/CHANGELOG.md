<!--
Release notes for go.temporal.io/sdk/contrib/gcp/gcsdriver.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
### Changed

- Claim metadata now identifies the stored object with `object_name` instead of
  `key`. Previous claims using `key` are still readable, so payloads already in 
  history continue to resolve. Support for the legacy field will be removed when 
  the driver reaches GA.
- Store and retrieve error messages now report `object_name=` instead of `key=`.

## [0.1.0] - 2026-08-13

### Added

- Initial release of the GCS storage driver for external payload storage,
  mirroring the existing S3 driver architecture (`contrib/aws/s3driver`).
