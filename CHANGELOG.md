<!--
High-level release notes.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

When your PR includes a user-facing change, add an entry below under the
appropriate heading (create the heading if it does not yet exist). Within
each heading content can be free-form. Feel free to include examples, links
to docs, or any other relevant information.

### Added            — new features
### Changed          — changes in existing functionality
### Deprecated       — soon-to-be-removed features
### Breaking Changes — removed or backwards-incompatible features
### Fixed            — notable bug fixes
### Security         — notable security fixes
-->

# Changelog

## [Unreleased]

### Added

* Exposed `BackoffStartInterval` when continuing as new, which will delay the first task of the
  continued workflow by the configured interval.

### Fixed

* `contrib/opentelemetry`: counter metrics are now emitted as a monotonic OpenTelemetry
  counter (`Int64Counter`) instead of an up/down counter (`Int64UpDownCounter`). Previously
  Temporal SDK counters such as `temporal_workflow_completed` were reported downstream as
  gauges (e.g. in Prometheus and Datadog) rather than counts, which could cause metric type
  conflicts. (#2140)
