# Proposed issue: Stopped workers retain sticky workflow cache state

Suggested labels: `bug`, `performance`

## Evidence status

The hypothesis is **confirmed** by the controlled experiment in `sticky-cache-retention-results.md`. Across fresh-process disabled, purged, and unpurged controls, stopped unpurged workers retained cached workflow state in every trial. At 32 workflows with 1 MiB of deterministic state each, unpurged processes retained an average 34,620,616 additional heap-object bytes relative to explicit purge, or 1,081,894 bytes per cached workflow. Explicit purge returned memory and workflow-coroutine counts to the disabled-cache control.

Representative `inuse_space` profile diffs attributed 32,770 KiB of the primary difference directly to workflow state under the SDK workflow-coroutine execution chain. A controlled 100-cycle workload also measured higher GC CPU per cycle in unpurged processes. These measurements establish local impact; they should not be extrapolated directly to arbitrary production workflow shapes.

## Expected Behavior

After a worker has fully stopped and the application releases it, workflow state cached on behalf of that worker should not keep the worker's cache lease alive indefinitely.

When the last worker using the process-wide sticky workflow cache stops, the SDK should release cached workflow execution state deterministically. Cleanup should not require an additional call to `worker.PurgeStickyWorkflowCache` or depend on when the garbage collector schedules a finalizer.

## Actual Behavior

The sticky workflow cache is process-global and uses a finalizer on `WorkerCache` to decrement `workerRefcount` and nil the shared cache when the final worker cache becomes unreachable:

- `sharedWorkerCachePtr` is a package global.
- `NewWorkerCache` installs a `runtime.SetFinalizer` callback.
- Cached `workflowExecutionContextImpl` values reference their `workflowTaskHandlerImpl`.
- `workflowTaskHandlerImpl` references the same `WorkerCache` carrying the finalizer.

This creates the following reachability path after a workflow execution has been cached:

```text
global sharedWorkerCachePtr
  -> workflowCache
  -> workflowExecutionContextImpl
  -> workflowTaskHandlerImpl
  -> WorkerCache
  -> sharedWorkerCachePtr
```

Because the path begins at a global GC root, the finalizable `WorkerCache` remains reachable through the cache it is intended to release. The experiment observed the corresponding workflow state and coroutine stacks still live after normal worker shutdown and normalized GC.

`AggregatedWorker.Stop` drains and stops child workers but does not explicitly release its `WorkerCache`. The existing `TestCreateAndFree` unit test calls `WorkerCache.close` directly, so it does not cover shutdown ownership, finalizer behavior, or a populated-cache reachability cycle.

The practical result is retention of stopped-worker workflow state until one of the following happens:

- the cached executions complete or are evicted through another path;
- the application explicitly calls `worker.PurgeStickyWorkflowCache` after stopping all workers; or
- another operation happens to break the reference graph.

This is bounded by the configured sticky cache capacity, but the default capacity is 10,000 workflow executions. Cached workflow state can be pointer-rich and may increase both retained heap and GC marking work.

The retained heap is bounded by cache capacity and entry sizes, but the experiment confirms that normal worker shutdown does not deterministically release it. The issue is ready for a deterministic failing lifecycle test and implementation work; heap-size assertions and finalizer timing should not be part of the permanent regression test.

## Completed Investigation

Run three otherwise identical scenarios in separate processes so the process-global cache cannot contaminate comparisons:

1. Sticky cache disabled.
2. Sticky cache enabled, followed by `worker.PurgeStickyWorkflowCache` after shutdown as a positive-release control.
3. Sticky cache enabled, without an explicit purge.

For each scenario:

1. Start multiple workflows whose deterministic in-memory state is large enough to appear clearly in a heap profile.
2. Wait until the sticky-cache-size metric confirms that the expected executions are cached.
3. Stop the worker, close the client, and release application references.
4. Force GC only to normalize the diagnostic snapshot.
5. record `/gc/heap/live:bytes`, `/gc/scan/heap:bytes`, and `/memory/classes/heap/objects:bytes` from `runtime/metrics`;
6. capture an `inuse_space` heap profile and inspect the retaining allocation stacks.

The full plan is in `sticky-cache-retention-investigation.md`; measurements and commands are in `sticky-cache-retention-results.md`.

A deterministic internal regression test would be preferable to a GC-timing-dependent test. It could populate the cache, stop the owning `AggregatedWorker`, and assert synchronously that the cache lease was released and that the final release clears cached execution state without calling `runtime.GC`.

## Specifications

- Version: current `main`; finalizer-based cache ownership was introduced in commit `d1ec38ed` / PR #310
- Platform: expected to be platform-independent
- Go version: module currently declares Go 1.25.4

## Relevant Code

- `internal/internal_worker_cache.go`: global cache, reference count, finalizer, and cleanup
- `internal/internal_task_handlers.go`: cached execution context and task-handler back-reference
- `internal/internal_worker.go`: `AggregatedWorker.Stop` lifecycle
- `internal/internal_worker_cache_test.go`: existing direct-close coverage
- `worker/worker.go`: public cache configuration and manual purge functions

## Investigation Completion Criteria

- Results include the disabled-cache, explicit-purge, and suspected-retention scenarios from fresh processes.
- A heap profile identifies what retains the workflow state; aggregate memory counters alone are insufficient.
- The report gives retained bytes and objects per cached workflow, with repeated runs and variability.
- GC impact is reported separately from retained heap and is not inferred from heap size alone.
- The hypothesis is explicitly marked confirmed, partially confirmed, or disproved.
- If confirmed, add a deterministic failing lifecycle test before implementing a fix.
- Only then select an ownership solution and define fix-specific acceptance criteria.
