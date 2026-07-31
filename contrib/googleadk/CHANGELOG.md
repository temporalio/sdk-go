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
  `NewReplaySafeMeterProvider` wrap OpenTelemetry providers so telemetry emitted from
  workflow code is recorded on first execution only. ADK records spans (including
  `gen_ai.usage.*` token attributes) and `gen_ai.*` log events through the OTel process
  globals from inside the workflow, so previously every history replay re-emitted one
  full copy of all of it. Install the wrappers as the first global providers set in the
  process; non-workflow telemetry passes through unchanged. See "Telemetry and replay"
  in the README.

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
