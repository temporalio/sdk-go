<!--
Release notes for go.temporal.io/sdk/contrib/opentelemetry-v2.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Initial release of `go.temporal.io/sdk/contrib/opentelemetry-v2`.
- `Tracer` / `NewReplaySafeTracerProvider` for replay-safe application spans inside
  workflows.
- `NewPlugin` / `PluginOptions` to enable OpenTelemetry tracing (and optional
  metrics) on clients and workers.
