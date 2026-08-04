<!--
Release notes for go.temporal.io/sdk/contrib/googleadk.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- `NewReplaySafeTracerProvider`, `NewReplaySafeLoggerProvider`, and
  `NewReplaySafeMeterProvider` wrap OpenTelemetry providers so history replays re-emit
  no workflow-side telemetry. ADK records spans (including `gen_ai.usage.*` token
  attributes) and `gen_ai.*` log events through the OTel process globals from inside
  the workflow, so previously every replay re-emitted one full copy of all of it.
  Install the wrappers as the first global providers set in the process; non-workflow
  telemetry passes through unchanged. Spans still open when their workflow leaves the
  worker are truncated (sticky-cache eviction) or lost (worker shutdown/crash) rather
  than duplicated — see "Telemetry and replay" in the README for the exact contract.
- `NewPlugin` logs a best-effort warning at worker start and workflow replayer
  creation when a global OpenTelemetry provider is a raw OTel SDK provider
  installed without a replay-safe wrapper.

## [0.2.0] - 2026-07-22

### Added

- `NewPlugin` wires the integration as a worker plugin: add it to `worker.Options.Plugins`
  to register the `InvokeModel` / `ListMcpTools` / `CallMcpTool` Activities at worker start
  and close cached MCP toolsets at worker stop. `NewActivities` + `Register` remain for the
  test environments (which do not run plugins) and manual wiring.

## [0.1.0] - 2026-07-20

### Added

- Added the `contrib/googleadk` package, which makes Google ADK (`adk-go`) agents durable and
  replay-safe under Temporal.
