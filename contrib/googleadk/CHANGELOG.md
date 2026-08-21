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
  query handlers and update validators always records. Workflow spans are
  re-created during replay as real spans (with workflow-time starts) and their
  `End` is suppressed while replaying: replays and eviction teardown export
  nothing, and a span cut off by sticky-cache eviction, worker shutdown, or
  crash is exported once by the catch-up replay's live `End`, complete with
  ADK's `gen_ai.usage.*` token attributes. See "Telemetry and replay" in the
  README.
- `NewPlugin` warns when a raw (unwrapped) OTel SDK provider is installed globally.

### Changed

- The deterministic UUID provider (`platform.WithUUIDProvider`, used for ADK's
  invocation/tool IDs) now draws from `workflow.GetRandomStream` instead of a
  `workflow.SideEffect`-seeded counter, dropping a per-workflow marker event and
  giving query/update-validator contexts ordinary random IDs. This changes the
  generated IDs and the command history: a workflow that recorded the SideEffect
  marker under an earlier release (v0.1.0 or v0.2.0) must finish on that release,
  because replaying it on the new provider is non-deterministic. Drain in-flight
  runs before upgrading.
- `NewReplaySafeTracerProvider` builds and owns its `sdktrace.TracerProvider`
  (`func(opts ...sdktrace.TracerProviderOption) *ReplaySafeTracerProvider`) and
  force-installs a deterministic span-ID generator drawn from
  `workflow.GetRandomStream`, so a span re-created on replay keeps its
  first-execution trace and span IDs and the generator can never be omitted
  (aligned with `contrib/opentelemetry-v2`).

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
