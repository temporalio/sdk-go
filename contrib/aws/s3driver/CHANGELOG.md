<!--
Release notes for go.temporal.io/sdk/contrib/aws/s3driver.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Changed

- `Client.GetObject` now returns `(io.ReadCloser, error)` instead of `([]byte, error)`. If you implement a custom `Client`, update your `GetObject` method to return a reader over the object data. The caller is responsible for closing the reader. Use `io.NopCloser(bytes.NewReader(data))` to wrap in-memory bytes.
- S3 object key path segments are now percent-encoded against S3's safe
  character set (alphanumerics and `!-_.*'()`), encoding all other bytes of
  their UTF-8 representation as `%XX`.

### Added

- `Options.MaxRetrieveSize` bounds memory allocation on the download path. Defaults to `max(MaxPayloadSize, 50 MiB)`. Objects exceeding this limit are rejected before being fully read into memory.
- `ErrPayloadTooLarge` sentinel error, returned when a retrieved object exceeds `MaxRetrieveSize`. Use `errors.Is(err, s3driver.ErrPayloadTooLarge)` to match.
