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
- Add support for Workflow Queries as Nexus Operations.

### Added

- Added `temporal.NewPayloadValidationError` to create non-retryable application errors with
  optional structured details for payload validation failures. Passing `nil` omits details.
- Added Go 1.27+ generic methods on the experimental `temporalnexus.NexusClient` for starting
  workflow-, activity-, and workflow-update-backed Nexus operations.
- Workflow task completions larger than the gRPC request size limit are now paginated automatically
  when the namespace supports it. Paginated workflow task completions require Temporal Server 1.32.0
  or later.
- The `temporal_activity_execution_failed` and `temporal_local_activity_execution_failed` worker
  metrics now carry a `failure_reason` attribute. Each is now split into one time series per
  reason, which may affect existing dashboards.

#### Standalone Activity operator commands

- `client.ActivityHandle` now supports operator commands for standalone activities: `Pause`,
  `Unpause`, `Reset`, `UpdateOptions` and `RestoreOriginalOptions`.
- Added opt-in payload fields to `client.DescribeActivityOptions`: `IncludeInput`,
  `IncludeOutcome`, `IncludeHeartbeatDetails` and `IncludeLastFailure`.
- Added missing description fields: `ExecutionTime` and `TotalHeartbeatCount`.

### :boom: Breaking Changes

- Description payload fields that previously came back unconditionally are now opt-in and must be
  requested via `client.DescribeActivityOptions`: `GetHeartbeatDetails` (`IncludeHeartbeatDetails`)
  and `GetLastFailure` (`IncludeLastFailure`).

### Changed

### Deprecated

### :boom: Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Experimental external storage: `converter.StorageDriverSelector.SelectDriver` now receives a
  `converter.StorageDriverSelectContext` instead of a `converter.StorageDriverStoreContext`.
  Update the parameter type; the new type carries the same `Context` and `Target` fields.
- Local activity results are now serialized with the local activity's `ActivitySerializationContext`
  (`IsLocal=true`) instead of the workflow serialization context. Users of a context-aware
  `DataConverter` or `PayloadCodec` whose encoding depends on the serialization context (for example
  context-derived encryption keys or AAD) may fail to decode local activity results recorded in
  histories written by earlier SDK versions, both on replay and when continuing an open workflow.
- Activity, local activity and child workflow serialization contexts are now applied to the
  worker-configured `DataConverter` and `FailureConverter` instead of the converter already carrying
  the current workflow context. A context-aware converter that composed contexts (deriving its state
  from both the workflow and the activity context) now sees only the activity or child workflow
  context, and one whose `WithSerializationContext` returned a converter that is no longer
  context-aware now receives the activity or child workflow context it previously never saw.

### Fixed

- Stand-alone activities started from a redelivered Nexus operation handler now reuse the Nexus
  request ID, preventing duplicate Nexus links when an idempotent start resolves to the original run.
- `temporal.IsWorkflowExecutionAlreadyStartedError` now detects wrapped
  `serviceerror.WorkflowExecutionAlreadyStarted` errors.
- Malformed Nexus link errors now log the link URL and parse error under stable structured fields.
- Local activity results are now serialized with the local activity's `ActivitySerializationContext`
  (`IsLocal=true`) on both ends. Previously the result was encoded with the plain worker data converter
  but decoded through the workflow serialization context, so a context-aware `DataConverter` or
  `PayloadCodec` saw mismatched contexts for local activity results.
- Workflow replays no longer retain one-shot workflow state in the process-wide sticky cache.
- Corrected stand-alone activity API documentation to use activity terminology, document that
  `GetActivityHandleOptions.RunID` may be empty to target the latest run, and describe
  `TerminateActivityOptions.Reason` as a termination reason.
- `DefaultFailureConverter.FailureToError` now correctly decodes `LastHeartbeatDetails` for a
  reset-workflow failure. Previously the raw payload proto was treated as a single detail value,
  so calling `Details()` on the resulting `ApplicationError` returned `ErrTooManyArg` instead of
  decoding it.

### Security

## [1.48.0] - 2026-08-18

### Added

- Added `-envconfig` support to the integration test harness, allowing integration tests to use
  standard client settings from `contrib/envconfig`.
- Added `client.Options.SdkName` and `client.Options.SdkVersion` to override the SDK name and version reported in worker heartbeats.
- Added `client.WorkflowRun.GetFirstExecutionRunID` to expose the first execution run ID returned by the server when starting a workflow.
- Added `client.Client.CancelWorkflowWithOptions` and `client.Client.TerminateWorkflowWithOptions` to target a workflow execution chain by its first execution run ID. Cancellation options can also specify a reason.
- Added experimental `workflow.GetRandomStream` for named deterministic pseudorandom values in workflows.
- Added experimental `workflow.IsReadOnly` to report whether the workflow context is in a read-only
  path.
- Added `go.temporal.io/sdk/interceptor/tracing`, a reworked tracing interceptor with
  corrected span parenting and span directions for span-kind mapping. It backs the new
  `contrib/opentelemetry-v2` module and is not span-compatible with the tracing interceptor
  used by `contrib/opentelemetry` (v1).

### Changed

- Improved the performance of yield-heavy workloads by eliminating unnecessary computation and heap allocations.
- Replaced the internal `OnceCell` implementation with `sync.OnceValue` for lazy workflow run ID lookup.

### Fixed

- Data converter errors raised while deserializing Nexus operation input are no longer replaced with
  a generic `BAD_REQUEST` handler error. A `temporal.ApplicationError` or a `nexus.HandlerError` is
  now propagated to the caller as-is, and any other error is wrapped in a `BAD_REQUEST`
  `nexus.HandlerError` that retains the original error as its cause. As an exception, a
  non-retryable `temporal.ApplicationError` with type `PayloadValidationError` is reported as a
  `BAD_REQUEST` `nexus.HandlerError` with the original error as its cause, since it indicates the
  operation input itself is invalid.
- Prevent workflow task failures when an activity with a custom ID completes while its cancellation
  command is pending.
- `TestWorkflowEnvironment.MutableSideEffect` now honors the provided equals function and only
  updates the recorded value when it changes, matching the real worker. Previously it ignored
  equals and returned a freshly computed value on every call.
- Nexus operation link propagation for stand-alone activities. When a Nexus operation handler uses
  `client.ExecuteActivity`, inbound Nexus request links are forwarded to the activity and the
  activity link returned by the server is propagated back to the Nexus operation caller.

## [1.47.0] - 2026-07-28

### Added

- Added `worker.Options.MaxEagerActivityReservationsPerWorkflowTask` to configure the maximum
  number of eager activity slots reserved per workflow task. The default remains three. Configured
  values must be positive; use `DisableEagerActivities` to disable eager activity execution.
- Automatically enroll workers into poller autoscaling when the namespace advertises the
  `PollerAutoscalingAutoEnroll` capability. This only applies to poller types left at their default
  (i.e. the worker set neither `MaxConcurrent<Type>TaskPollers` nor `<Type>TaskPollerBehavior`);
  explicitly configured pollers are left unchanged.
- Added `worker.Options.PreferredVersionProvider`, which can select the version recorded by a
  newly encountered `workflow.GetVersion` call. This supports gradual rollout of a new
  `GetVersion` call before activating its new behavior.
- Add support for Workflow Updates as Nexus Operations 
- Add support for external storage to Nexus task handling.

### Changed

- User metadata fields (StaticSummary, StaticDetails, CurrentDetails, Activity Summary, Timer
  Summary, AwaitOptions) are no longer marked as experimental.
- Send the initial Worker heartbeat immediately on startup, include the client identity, and omit
  elapsed-since-last-heartbeat until a previous heartbeat exists.

### Fixed

- Prevent a background panic during worker shutdown when the local activity tunnel closes while a
  poller is waiting for a task.
- Allow query results to use external storage before payload size enforcement.
- Correct schedule catch-up window documentation to state that an unset value is omitted and the
  server applies its one-year default.
- Resource-based tuner: `ReserveSlot` now honors context cancellation while the resource controller is
  declining slots. Previously the retry loop observed the context only while the ramp throttle was making
  the caller wait, so a poller goroutine could outlive worker shutdown, keeping the worker's stop
  `WaitGroup` from draining and continuing to sample system resources for the life of the process.
- Resource-based tuner: `TryReserveSlot` (used for eager task dispatch) no longer blocks for up to
  `RampThrottle` while a concurrent `ReserveSlot` waits out the ramp throttle. The throttle behavior
  is unchanged; only the unnecessary lock contention on the eager path is removed.
- Stand-alone activity-backed Nexus operations. `temporalnexus.MustNewTemporalOperation` can now
  back an async Nexus operation with a stand-alone activity execution via `StartActivity` /
  `StartUntypedActivity`. Activity-backed Nexus operations are also supported in `TestWorkflowEnvironment`.
- Fixed worker task slot metrics reporting stale values when slot state changes concurrently.
- Dynamic workflows registered as a `WorkflowDefinitionFactory` are now executed via
  `NewWorkflowDefinition()` rather than being wrapped as a function and reflected on (which panicked
  with `reflect: call of reflect.Value.Call on ptr Value`), in both the worker registry and the test
  environment. This lets host processes that register a single shared factory (e.g.
  `roadrunner-temporal` / the PHP SDK) use dynamic workflows.
- Merged link-converter class in the server and sdk-go and moved it to api-go
- Nexus operations with `NexusOperationCancellationTypeAbandon` no longer panic the workflow task when
  the operation later starts or completes after the caller is canceled.
- Session worker: stopping a worker while it is at its maximum concurrent session count no longer blocks
  for the stop timeout. The session creation poller waited for an available session token without
  observing the stop signal, so shutdown could not interrupt it and the poller goroutine leaked.

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
