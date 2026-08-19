# Sticky workflow cache lifecycle design

## Status

Evidence-backed candidate design. The experiment in `sticky-cache-retention-results.md` confirmed the retention path: unpurged stopped workers retained cached workflow state and coroutine stacks, explicit purge returned them to control, and profile diffs attributed the dominant retained bytes to the workflow coroutine execution chain. No implementation has been made.

## Summary

The sticky workflow cache should use explicit lease ownership tied to worker lifecycle. `AggregatedWorker.Stop` should release its lease after task processing and child-worker shutdown have completed. Garbage-collector cleanup may remain as a fallback, but it should not be the normal cleanup mechanism.

The lease object should also be removed from the object graph stored inside the cache. Otherwise a finalizer or `runtime.AddCleanup` replacement alone cannot solve the reachability cycle: cleanup callbacks only become eligible after their associated object becomes unreachable.

The profiling result supports this ownership split. At the primary experiment point, unpurged shutdown retained about 34.62 MB more heap-object memory than explicit purge, while the existing global-cache/task-handler/`WorkerCache` graph explains why finalizer cleanup cannot become eligible. Implementation should still begin with the deterministic failing lifecycle tests below, because heap and finalizer timing are unsuitable CI assertions.

## Current Ownership

`NewAggregatedWorker` creates one `WorkerCache`. Copies of `workerExecutionParameters` pass that pointer into the workflow task handler. Each cached workflow execution context points back to that handler.

The current graph is therefore:

```text
AggregatedWorker --------------------------------------+
  -> executionParams.cache                             |
                                                      v
global shared cache -> cached execution -> handler -> WorkerCache lease
        ^                                             |
        +---------------------------------------------+
```

The shared cache is deliberately process-wide so live workers can share sticky workflow state. The problem is not global sharing by itself; it is using an object reachable from the cache contents as the finalizable token responsible for releasing that cache.

## Goals

- Release stopped-worker cache ownership synchronously.
- Preserve process-wide cache sharing among concurrently running workers.
- Clear cached workflow state only after the final live worker lease is released.
- Keep cleanup idempotent and race-free.
- Preserve public API compatibility and sticky execution behavior.
- Ensure eviction cleanup closes workflow execution state.

## Non-goals

- Replacing the LRU implementation.
- Changing the default cache size.
- Changing sticky task routing or workflow replay semantics.
- Adding a new public `Close` method to `worker.Worker`.
- Tuning `GOGC`, `GOMEMLIMIT`, or application runtime settings.

## Recommended Design

### 1. Separate the cache handle from its ownership lease

Introduce two internal concepts:

- A shared cache handle used by workflow task handlers for `Get`, `Put`, `Delete`, size, and capacity operations.
- A per-worker lease owned by `AggregatedWorker`, responsible only for decrementing the shared owner count exactly once.

Cached workflow contexts and their task handlers may retain the shared handle, but must not retain the per-worker lease. This removes the cleanup token from the globally rooted cache graph.

One possible shape is:

```go
type workerCacheLease struct {
    once   sync.Once
    shared *sharedWorkerCache
    lock   *sync.Mutex
}

type workflowCacheHandle struct {
    shared *sharedWorkerCache
}
```

The exact names are not important. The important invariant is:

> Objects stored in the shared cache must not reference the lease whose release is required to destroy that cache.

### 2. Release explicitly from worker shutdown

After `AggregatedWorker.Stop` has stopped pollers, drained task processing, stopped child workers, completed plugin shutdown, and unregistered heartbeat state, it should release its cache lease.

The release operation should be guarded by `sync.Once` so that:

- repeated internal cleanup attempts cannot decrement the owner count twice;
- partial-start and start-failure cleanup can use the same path;
- a GC fallback cannot race with or repeat explicit cleanup.

Normal correctness must not depend on the finalizer.

### 3. Clear state on the final release

When the reference count reaches zero:

1. Detach the current LRU instance from the process-global holder while holding the ownership lock.
2. Clear the detached cache so its existing `RemovedFunc` runs and workflow event handlers are closed.
3. Ensure a concurrently created worker receives a new cache instance and cannot accidentally use the detached instance.

Lock ordering needs care. Prefer detaching under `sharedWorkerCacheLock` and clearing outside that lock if the removal callback or future callback changes could acquire SDK locks. This also avoids holding the process-global lock while an arbitrarily large cache is traversed.

For example, the release operation can conceptually do this:

```text
lock shared ownership
decrement lease count
if count becomes zero:
    save old cache locally
    set shared cache to nil
unlock shared ownership
clear saved cache
```

Before using this sequence, verify that no cache operation can begin after worker shutdown has drained. If cache access can race with release, the handle needs its own synchronization or a generation object that remains valid until all operations finish.

### 4. Keep GC cleanup only as a safety net

Options, in preference order:

1. Explicit release plus a GC fallback for callers that construct an internal worker but never stop it.
2. Explicit release only, if all creation and failure paths can be proven to have deterministic ownership.

Replacing `runtime.SetFinalizer` with `runtime.AddCleanup` may make fallback code less error-prone on the repository's supported Go version, but it does not by itself fix a reachability cycle. The associated lease must first be absent from cached values.

If a fallback remains, its callback arguments and closures must not refer back to the lease object. The explicit and fallback paths must share the same once-only release state.

### 5. Cover every acquisition path

Audit each `NewWorkerCache` caller and pair it with ownership:

- Normal `AggregatedWorker`: release after successful shutdown.
- Start failure or partially initialized worker: release after all started components have been stopped.
- Workflow replayer: use scoped ownership, normally `defer lease.Release()` around replay processing.
- Unit-test helpers and internal direct constructors: release explicitly in test cleanup.

This audit is important because fixing only `AggregatedWorker.Stop` could leave replay or construction-error paths dependent on finalization.

## Alternative Designs

### Clear the global cache directly from every `Worker.Stop`

Rejected. Multiple live workers share the cache, so one worker stopping must not evict state used by another worker.

### Keep the current graph and replace `SetFinalizer` with `AddCleanup`

Rejected as a complete solution. A cleanup mechanism cannot run while its associated object is reachable through the global cache.

### Require users to call `worker.PurgeStickyWorkflowCache`

Rejected. Manual purge is useful operationally, but normal resource ownership should follow the worker lifecycle. It is also unsafe to purge while another worker is active.

### Make the cache worker-local

Not recommended for this fix. It would be a larger behavioral and performance change because current workers in the same process intentionally share sticky workflow state.

### Remove all GC fallback behavior

Possible after all acquisition and failure paths are paired with deterministic release. This produces the simplest ownership model but gives no protection when application code abandons a worker without stopping it. Decide this separately from the primary fix.

## Testing Plan

### Focused unit tests

- Two leases share one cache; releasing the first keeps the cache alive.
- Releasing the last lease clears and detaches the cache synchronously.
- Releasing the same lease twice has no effect after the first release.
- Cache removal callbacks run when the final lease is released.
- A newly acquired lease after final release receives a fresh cache generation.
- A handler stored in the cache does not retain the ownership lease.
- Concurrent acquire/release operations pass under the race detector.
- No test requires `runtime.GC`, sleeps, or eventual finalizer execution.

### Worker lifecycle tests

- A started worker that cached an incomplete workflow releases its lease on `Stop`.
- A worker stopped before `Start` does not leak a lease.
- A `Start` failure releases its lease after partial cleanup.
- Two workers can run concurrently; stopping one preserves the other's cached state.
- The workflow replayer releases its scoped lease for complete and incomplete histories.

### Performance validation

Create a benchmark or diagnostic test that repeatedly:

1. creates a worker;
2. caches representative workflow state;
3. stops and discards the worker.

Compare before/after heap profiles and GC metrics. The desired result is a flat retained-heap trend after shutdown without calling `worker.PurgeStickyWorkflowCache`. Allocation rate during normal workflow task processing should not regress materially.

Reuse the opt-in harness in `test/sticky_cache_retention_profile_test.go` for before/after evidence. The confirmed baseline and exact commands are recorded in `sticky-cache-retention-results.md`.

### Repository validation

From `internal/cmd/build`:

```bash
go run . check
go run . unit-test -run "TestWorkerCache|Test.*Worker.*Stop"
go run . integration-test -dev-server -run "TestIntegrationSuite/<focused-test>"
```

For workflow execution and cache behavior, run the focused integration coverage in both default-cache mode and with `WORKFLOW_CACHE_SIZE=0`.

## Compatibility and Release Notes

- Public API: no change required.
- Workflow determinism: no intended change; cleanup happens only after worker processing has drained.
- Wire compatibility: no change.
- User-visible behavior: stopped workers should release cached memory sooner and deterministically.
- Changelog: add an entry under `## [Unreleased]` describing the stopped-worker sticky-cache retention fix if the hypothesis is confirmed.

## Open Questions

- Can any workflow cache operation continue after all child `Stop` calls return, especially when `WorkerStopTimeout` expires?
- Should final release call `Clear` synchronously, or should it detach synchronously and perform potentially expensive event-handler cleanup asynchronously?
- Are worker instances intended to remain restartable after `Stop` anywhere in internal tests or unsupported usage?
- Should the cache lease be acquired during construction or only once `Start` commits successfully?
- Does replay ever cache an incomplete execution context long enough to exercise the same retention graph?
- Would per-generation cache state simplify locking compared with mutating one permanent global holder?

## Suggested Implementation Sequence

1. ~~Run the controlled integration/profile experiment in `sticky-cache-retention-investigation.md`.~~ Completed; see `sticky-cache-retention-results.md`.
2. ~~Record the retaining allocation path, retained bytes per cached workflow, and GC-cost measurements.~~ Completed.
3. ~~Stop if the hypothesis is disproved; update the issue with the evidence.~~ The hypothesis was confirmed and the issue draft was updated.
4. Add a deterministic failing internal lifecycle test that does not depend on finalizer timing.
5. Split cache access from lease ownership without changing public APIs.
6. Add idempotent explicit release to all ownership paths.
7. Clear and detach the cache on final release, preserving removal callbacks.
8. Decide whether to retain `SetFinalizer`, migrate the fallback to `AddCleanup`, or remove the fallback.
9. Repeat the same profile experiment and run focused unit, race, and integration validation.
10. Add the changelog entry once the behavior is fixed and the before/after evidence confirms the result.
