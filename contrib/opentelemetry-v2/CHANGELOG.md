<!--
Release notes for go.temporal.io/sdk/contrib/opentelemetry-v2.
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

### Fixed

- Workflow spans no longer end with an error status (or a recorded exception event) when the
  workflow returns `ContinueAsNewError`. Continue-as-new is normal control flow, not a failure.

## [0.1.0]

### Added

- Initial release of `go.temporal.io/sdk/contrib/opentelemetry-v2`.
- `Tracer` / `NewReplaySafeTracerProvider` for replay-safe application spans inside
  workflows.
- `NewPlugin` / `PluginOptions` to enable OpenTelemetry tracing (and optional
  metrics) on clients and workers.
