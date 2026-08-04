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

## [0.1.0] - 2026-08-13
### Changed
- `Client.GetObject` now returns `(io.ReadCloser, error)` instead of `([]byte, error)`. If you implement a custom `Client`, update your `GetObject` method to return a reader over the object data. The caller is responsible for closing the reader. Use `io.NopCloser(bytes.NewReader(data))` to wrap in-memory bytes.

### Added
- `Options.MaxRetrieveSize` bounds memory allocation on the download path. Defaults to `max(MaxPayloadSize, 50 MiB)`. Objects exceeding this limit are rejected before being fully read into memory.
- `ErrPayloadTooLarge` sentinel error, returned when a retrieved object exceeds `MaxRetrieveSize`. Use `errors.Is(err, gcsdriver.ErrPayloadTooLarge)` to match.

- Initial release of the GCS storage driver for external payload storage,
  mirroring the existing S3 driver architecture (`contrib/aws/s3driver`).
