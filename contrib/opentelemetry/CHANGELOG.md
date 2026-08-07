<!--
Release notes for go.temporal.io/sdk/contrib/opentelemetry.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

## [0.8.1] - 2026-07-21

### Fixed

- Corrected the module's minimum Temporal Go SDK requirement from v1.12.0 to v1.46.0. The stale
  minimum could select SDK v1.12.0 alongside the modern Temporal API, gRPC, protobuf, and split
  `genproto` dependencies used by this release, producing compilation failures or, in legacy
  dependency graphs, ambiguous `genproto` imports. Applications already selecting a compatible
  newer SDK are unaffected; this does not resolve `genproto` conflicts introduced by unrelated
  dependencies.

## [0.8.0] - 2026-07-16

### Added

- Added `UseMonotonicCounters` to `MetricsHandlerOptions`. When enabled, SDK counters use
  OpenTelemetry monotonic counters so exporters can classify them as counters. The option defaults
  to false to preserve existing metric types and names. Custom counters used with this option must
  not decrement; doing so may produce invalid or backend-dependent metric data.

### Changed

- Increased the module's minimum required Go version from 1.23 to 1.25.4.
