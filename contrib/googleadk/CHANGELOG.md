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
  telemetry emitted from workflow code is not re-emitted on history replay. See
  "Telemetry and replay" in the README.
- `NewPlugin` warns when a raw (unwrapped) OTel SDK provider is installed globally.

### Changed

- Bumped the `google.golang.org/adk/v2` requirement to v2.2.0, the first tagged release with
  the request-order confirmation resume (google/adk-go#1169). Multi-decision tool-confirmation
  resumes are now replay-stable: `ConfirmationResponse` may batch any number of decisions in
  one Run pass — the previous one-decision-per-pass guidance for Activity-dispatching tools is
  lifted — and the resulting Activities are scheduled in the request order of the confirmations
  (newest pause event first when one batch spans several).
- Minimum `google.golang.org/genai` is now v1.66.0 (raised by adk/v2 v2.2.0; previously
  v1.57.0). `genai` types appear in this module's API — e.g. `ConfirmationResponse` returns
  `*genai.Content`.

### Breaking Changes

- The module now requires Go 1.26.5+ (inherited from `google.golang.org/adk/v2` v2.2.0's `go`
  directive; previously 1.25.0).

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
