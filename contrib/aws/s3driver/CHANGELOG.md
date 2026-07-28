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

- S3 object key path segments are now percent-encoded against S3's safe
  character set (alphanumerics and `!-_.*'()`), encoding all other bytes of
  their UTF-8 representation as `%XX`.
