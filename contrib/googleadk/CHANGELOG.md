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
  `NewReplaySafeMeterProvider`: wrap the global OpenTelemetry providers so ADK
  telemetry emitted from workflow code is not re-emitted on history replay;
  while suppressed, the logger's and sync instruments' `Enabled` report false.
  The gate composes `workflow.IsReplaying` with `!workflow.IsReadOnly`
  (Experimental), so telemetry from
  query handlers and update validators always records. See "Telemetry and
  replay" in the README.
- `NewPlugin` warns when a raw (unwrapped) OTel SDK provider is installed globally.

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
