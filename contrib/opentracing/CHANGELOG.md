<!--
Release notes for go.temporal.io/sdk/contrib/opentracing.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- `TracerOptions.EncodeHeaders` opts tracing headers into codec processing using the configured
  client's payload codec. Defaults to false; application-supplied headers are unchanged.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
