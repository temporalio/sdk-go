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

- Initial release of `go.temporal.io/sdk/contrib/opentelemetry-v2`. The module
  is experimental: APIs may change in incompatible ways between releases.
- `NewTracer` / `NewTracerProvider` for replay-safe application spans inside
  workflows. Sequenced spans consume a shared deterministic id stream; `End` is
  a no-op during replay. `StartUnsequenced` covers spans started outside the
  workflow event stream (query handlers, update validators): it preserves
  parenting without consuming the id stream.
- `NewPlugin` / `PluginOptions` to enable OpenTelemetry tracing (and optional
  metrics) on a client; workers created from that client pick it up
  automatically. `NewPlugin` returns a shutdown function that flushes and shuts
  down the provider it owns. `PluginOptions.ProviderOptions` configures that
  provider.
- Deterministic workflow span/trace IDs are produced automatically by the
  plugin. Client and activity spans use random IDs.
