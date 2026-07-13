<!--
Release notes for go.temporal.io/sdk/contrib/opentelemetry.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Added `UseMonotonicCounters` to `MetricsHandlerOptions`. When enabled, SDK counters use
  OpenTelemetry monotonic counters so exporters can classify them as counters. The option defaults
  to false to preserve existing metric types and names. Custom counters used with this option must
  not decrement; doing so may produce invalid or backend-dependent metric data.
