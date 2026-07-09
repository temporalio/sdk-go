<!--
High-level release notes for the main Go SDK module. Changes to independently
released modules under `contrib` belong in the `CHANGELOG.md` for that module.
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

- Added `worker.Options.PreferredVersionProvider`, which can select the version recorded by a
  newly encountered `workflow.GetVersion` call. This supports gradual rollout of a new
  `GetVersion` call before activating its new behavior.

### Fixed

- Correct schedule catch-up window documentation to state that an unset value is omitted and the
  server applies its one-year default.
- Resource-based tuner: `ReserveSlot` now honors context cancellation while the resource controller is
  declining slots. Previously the retry loop observed the context only while the ramp throttle was making
  the caller wait, so a poller goroutine could outlive worker shutdown, keeping the worker's stop
  `WaitGroup` from draining and continuing to sample system resources for the life of the process.
- Resource-based tuner: `TryReserveSlot` (used for eager task dispatch) no longer blocks for up to
  `RampThrottle` while a concurrent `ReserveSlot` waits out the ramp throttle. The throttle behavior
  is unchanged; only the unnecessary lock contention on the eager path is removed.

## [1.46.0] - 2026-07-07

### Fixed

- Respect SDK flags already recorded in workflow history even when `GetSystemInfo` does not report
  SDK metadata support.
- Only treat `GetSystemInfo` `UNIMPLEMENTED` responses as missing server capability support when
  the error indicates an unknown method.
- Retry server RPCs without gzip compression when a method reports that gzip decompression is
  unsupported, while continuing to use gzip for other methods.
- Populate `Priority` on `ScheduleWorkflowAction` values returned by `ScheduleHandle.Describe()`.
- Report the configured deadlock detection timeout in potential deadlock errors instead of always
  saying "over a second".
- Register all poller types before starting autoscaling pollers to avoid an autoscaling worker
  startup race.
- Treat `workflow.SideEffectWithOptions` and `workflow.MutableSideEffectWithOptions` as valid
  deterministic wrappers in `workflowcheck`.

### Added

- Added `OneTimeVersioningOverride` support for workflow start and workflow execution options,
  allowing a workflow to route to a target Worker Deployment Version until one Workflow Task
  completes there.
- Nexus operation link propagation for signals. When a Nexus operation handler signals a workflow
  (including signal-with-start), the inbound Nexus request links are now forwarded onto the signaled
  workflow so its history events link back to the caller, and the link the server returns for the
  signaled event is attached to the caller workflow's Nexus operation history event. This makes the
  caller and callee mutually navigable in the UI for signal-based Nexus operations.
- Support propagating standalone Nexus operation links.
- OpenTelemetry tracing support for standalone activities started from the client.
- Doclink now links interfaces when they're re-exported from `private` to a public package.
