<!--
Release notes for go.temporal.io/sdk/contrib/opentracing.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Changed

- Added `TracerOptions.UseDataConverter` to opt into using the configured data
  converter, including payload codecs, for Temporal trace headers. The default
  remains the SDK's default converter. Custom converters must support
  `map[string]string`, and all participants, including replay, must use compatible
  converters. Nexus text headers are unchanged.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
