# Sticky workflow cache retention results

## Conclusion

**Confirmed.** On SDK commit `0feb5a6813ab94b6e790260521a94914bb5e3267`, stopping a worker with incomplete workflows in the process-wide sticky cache does not release those workflow executions. The unpurged processes retained the workflow state, workflow coroutines, and associated SDK structures after the worker and client references were dropped and two GCs completed. Explicitly calling `worker.PurgeStickyWorkflowCache()` returned all measured values to the cache-disabled control.

At the primary 32-workflow, 1 MiB-state point, the unpurged scenario retained an average **34,620,616 bytes more heap-object memory than the purged control**, or **1,081,894 bytes per cached workflow**. At the smaller 16-workflow, 512 KiB-state point, it retained **9,329,725 bytes more**, or **583,108 bytes per workflow**. The 24 MiB reduction in requested workflow state produced a 25.29 MiB reduction in the measured retained difference, so the retained heap tracks the cached workflow state rather than fixed process noise.

This is a sticky-cache lifecycle/finalizer reachability problem, not evidence that package `init` functions generally cause this behavior. The retained byte slices in this experiment are pointer-free, but the cached workflow runtime surrounding them adds scannable objects and measurable GC work.

## Environment and method

- Go: `go1.25.4`
- OS/architecture: `darwin/arm64`
- `GOMAXPROCS`: 14
- SDK commit: `0feb5a6813ab94b6e790260521a94914bb5e3267`
- `GOGC`, `GOMEMLIMIT`, and `GODEBUG`: unset
- Dev server: Temporal CLI `1.7.2-one-time-versioning-override`, server `1.32.0-158.0`, managed and stopped by `internal/cmd/build`
- Final artifact runs: `.sticky-cache-retention-artifacts/20260819T030426Z` and `.sticky-cache-retention-artifacts/20260819T030452Z`

The opt-in integration harness in `test/sticky_cache_retention_profile_test.go` compiles a disposable test binary and starts each scenario/trial as a fresh Go process. Each process starts and queries every workflow before shutdown, observes the sticky-cache-size metric, stops the worker, closes and drops the client, applies the scenario-specific purge, performs two normalization GCs, records `runtime/metrics`, and captures an `inuse_space` profile. The parent removes the disposable test binary.

The three scenarios were:

| Scenario | Cache configuration | Post-stop action |
| --- | ---: | --- |
| `disabled` | 0 | none |
| `purged` | workload count + 2 | `worker.PurgeStickyWorkflowCache()` |
| `unpurged` | workload count + 2 | none |

Every final scenario has three fresh-process trials. Cache observations were 0/0/0 for disabled, 32/32/32 or 16/16/16 for purged, and 32/32/32 or 16/16/16 for unpurged. Shutdown-to-snapshot was 4.4-8.4 ms except for two purged scale trials at 25-27 ms; those trials' memory values still agreed with their control peers.

## Memory results

Values below are post-stop minus the same process's pre-workload baseline. Each cell is the median with the three-trial range in parentheses.

### Primary workload: 32 workflows × 1 MiB state

| Scenario | Live heap bytes | Heap objects | Scannable heap bytes | Heap-object bytes | Workflow coroutines after stop |
| --- | ---: | ---: | ---: | ---: | ---: |
| disabled | 2,061,336 (2,027,064-2,070,088) | 7,933 (7,912-7,972) | 1,499,016 (1,496,912-1,512,216) | 2,054,344 (2,028,248-2,073,656) | 0/0/0 |
| purged | 2,051,896 (1,994,232-2,056,280) | 7,921 (7,915-7,951) | 1,508,336 (1,506,040-1,511,176) | 2,051,432 (1,991,928-2,056,280) | 0/0/0 |
| unpurged | 36,663,672 (36,626,968-36,668,488) | 11,284 (11,283-11,296) | 2,508,520 (2,502,656-2,512,832) | 36,666,160 (36,626,896-36,668,432) | 32/32/32 |

Using averages to avoid selecting one control trial, unpurged minus purged was:

- live heap: 34,618,907 bytes, or 1,081,841 bytes/workflow;
- heap-object memory: 34,620,616 bytes, or 1,081,894 bytes/workflow;
- heap objects: 3,359 objects, or about 105 objects/workflow;
- scannable heap: 999,485 bytes, or about 31,234 bytes/workflow.

### Scale workload: 16 workflows × 512 KiB state

| Scenario | Live heap bytes | Heap objects | Scannable heap bytes | Heap-object bytes | Workflow coroutines after stop |
| --- | ---: | ---: | ---: | ---: | ---: |
| disabled | 2,031,904 (1,998,888-2,033,920) | 7,894 (7,847-7,903) | 1,497,624 (1,489,208-1,507,064) | 2,025,984 (1,989,064-2,035,856) | 0/0/0 |
| purged | 2,033,120 (2,008,544-2,037,312) | 7,820 (7,799-7,821) | 1,485,248 (1,484,096-1,486,480) | 2,032,672 (2,004,768-2,033,120) | 0/0/0 |
| unpurged | 11,376,072 (11,291,296-11,389,720) | 9,629 (9,610-9,631) | 2,399,040 (2,391,048-2,400,200) | 11,382,488 (11,289,816-11,387,432) | 16/16/16 |

Average unpurged minus purged heap-object memory was 9,329,725 bytes, or 583,108 bytes/workflow. The result includes each workflow's requested 524,288-byte state plus cached SDK execution overhead. The overhead-per-workflow estimate is workload-dependent, so the primary 1,081,894-byte figure should not be extrapolated as a universal SDK constant.

## Heap-profile attribution

Full heap sampling (`runtime.MemProfileRate = 1`) was enabled before workload allocation. In representative primary trial 2, the unpurged profile had 35,858.50 KiB in use. `stickyCacheRetentionProfileWorkflow` accounted for 32,770 KiB flat (91.39%). Its cumulative allocation chain was:

```text
internal.(*coroutineState).run
  -> internal.(*syncWorkflowDefinition).Execute.func1
  -> internal.(*workflowEnvironmentInterceptor).ExecuteWorkflow
  -> internal.(*workflowExecutor).Execute
  -> internal.executeFunction
  -> stickyCacheRetentionProfileWorkflow
```

Diffing unpurged against the matching purged trial attributed 32,770 KiB (95.34% of the positive profile difference) directly to the workflow function and 784.24 KiB to `internal.newLocalActivityTunnel`; the same coroutine execution chain retained the workflow allocation. The purged and disabled profiles did not retain the workflow allocation. The scale diff independently attributed 8,193 KiB to the workflow function, matching 16 × 512 KiB.

Heap profiles show allocation stacks, not GC root-to-object paths. The retaining owner is established by combining the profile/control result with the source graph and lifecycle observation:

```text
global sharedWorkerCachePtr
  -> workflowCache
  -> workflowExecutionContextImpl
  -> workflowTaskHandlerImpl.cache
  -> WorkerCache
  -> sharedWorkerCachePtr
```

`NewWorkerCache` increments `workerRefcount` and installs a finalizer; `WorkerCache.close` is the only decrement path. `AggregatedWorker.Stop` does not call it. Because cached task handlers reference the `WorkerCache`, the finalizable object remains reachable from the global cache. The 32 or 16 workflow coroutine stacks remaining only in unpurged trials corroborate that the cached executions, not client/run handles or the server, are live. Explicit purge removes the cached values and returns both heap and coroutine counts to control.

## Controlled GC impact

After the heap snapshot, each child normalized GC again, disabled automatic GC for the controlled phase, and repeated exactly 100 iterations of: allocate and touch 8 MiB, keep it alive through the allocation step, then call `runtime.GC`. Every final trial recorded exactly 100 completed cycles. CPU is the delta of `/cpu/classes/gc/total:cpu-seconds` divided by completed cycles; wall time is the aggregate loop duration.

| Workload | Scenario | GC CPU/cycle median (range) | Wall time for 100 cycles median (range) |
| --- | --- | ---: | ---: |
| 32 × 1 MiB | disabled | 2.926 ms (2.668-3.038) | 71.032 ms (70.913-76.987) |
| 32 × 1 MiB | purged | 2.701 ms (2.491-2.741) | 71.548 ms (68.405-71.953) |
| 32 × 1 MiB | unpurged | 3.549 ms (3.440-3.751) | 89.181 ms (82.577-90.234) |
| 16 × 512 KiB | disabled | 2.663 ms (2.632-2.668) | 71.813 ms (68.671-71.855) |
| 16 × 512 KiB | purged | 2.689 ms (2.657-2.737) | 71.442 ms (70.392-72.002) |
| 16 × 512 KiB | unpurged | 3.224 ms (3.084-3.290) | 83.041 ms (80.459-85.701) |

Relative to purge, median GC CPU/cycle was 31.4% higher at the primary point and 19.9% higher at the scale point. Median wall time was 24.7% and 16.2% higher, respectively. The ranges do not overlap within either workload size. This confirms measurable GC cost in this controlled forced-GC workload; it does not predict an application's production GC percentage because real workflow state pointer density, allocation rate, `GOGC`, memory limits, CPU availability, and background work differ.

## Deterministic regression test proposal

Do not make a permanent test depend on finalizer timing, heap size, goroutine stack text, or `runtime.GC`. First separate cache access from per-worker ownership as proposed in `sticky-cache-lifecycle-design.md`, then test the ownership lifecycle directly:

1. Create two worker cache leases against an isolated shared-cache generation.
2. Insert a representative cached execution through the normal cache handle.
3. Release the first lease and synchronously assert that owner count remains one and the cached execution remains available.
4. Release the second lease and synchronously assert owner count zero, the shared generation is detached, and removal/close callbacks ran.
5. Release either lease again and assert idempotence.
6. Add a focused `AggregatedWorker.Stop` test that caches an incomplete workflow task, stops normally, and asserts its lease was synchronously released.

The core invariant is that objects stored in the shared cache must not reference the lease whose release is required to destroy that cache.

## Commands and validation

Authoritative measurement commands, run from `internal/cmd/build`:

```bash
TEMPORAL_STICKY_CACHE_PROFILE=1 TEMPORAL_STICKY_CACHE_PROFILE_GC_CYCLES=100 go run . integration-test -dev-server -run '^TestStickyCacheRetentionProfile$'
TEMPORAL_STICKY_CACHE_PROFILE=1 TEMPORAL_STICKY_CACHE_PROFILE_WORKFLOWS=16 TEMPORAL_STICKY_CACHE_PROFILE_STATE_BYTES=524288 TEMPORAL_STICKY_CACHE_PROFILE_GC_CYCLES=100 go run . integration-test -dev-server -run '^TestStickyCacheRetentionProfile$'
```

The repository command wrapper used was `rtk env ...` around each command. Both passed under the canonical race-enabled integration runner. The embedded dev-server logs were copied into each final artifact directory and end with the expected graceful-stop transport shutdown.

Focused normal-suite compile/skip validation, run from `test`:

```bash
go test -run '^TestStickyCacheRetentionProfile$' .
```

This passed; without the opt-in environment variable the profiling test skips.

Repository validation, run from `internal/cmd/build`:

```bash
go run . check
```

This passed (`go vet`, `errcheck`, `staticcheck`, and doc-link validation).

Representative profile commands, run from the repository root because this Go toolchain did not expose `go tool pprof`:

```bash
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -nodecount=20 -sample_index=inuse_space .sticky-cache-retention-artifacts/20260819T030426Z/unpurged-2.pprof
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -cum -nodecount=20 -sample_index=inuse_space .sticky-cache-retention-artifacts/20260819T030426Z/unpurged-2.pprof
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -nodecount=20 -sample_index=inuse_space -base .sticky-cache-retention-artifacts/20260819T030426Z/purged-2.pprof .sticky-cache-retention-artifacts/20260819T030426Z/unpurged-2.pprof
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -nodecount=15 -sample_index=inuse_space -base .sticky-cache-retention-artifacts/20260819T030452Z/purged-2.pprof .sticky-cache-retention-artifacts/20260819T030452Z/unpurged-2.pprof
```

Earlier exploratory runs were discarded after they revealed two harness issues: an imprecise shutdown timer and automatic GCs mixed into the fixed-cycle GC phase. The final runs above include the corrected timing, full heap sampling, and exactly 100 forced cycles in every trial.

## Files and limitations

Created or intentionally modified for this investigation:

- `test/sticky_cache_retention_profile_test.go`
- `sticky-cache-retention-results.md`
- `sticky-cache-gc-issue.md`
- `sticky-cache-lifecycle-design.md`
- `sticky-cache-retention-investigation.md`
- `sticky-cache-retention-goal.md` (pre-existing goal draft, unchanged by the experiment)
- `.sticky-cache-retention-artifacts/manifest.md`
- `.sticky-cache-retention-artifacts/20260819T030426Z/*`
- `.sticky-cache-retention-artifacts/20260819T030452Z/*`

Residual limitations:

- The synthetic state is a large pointer-free byte slice. Real workflow state can have very different retained and scannable ratios.
- Heap profiles identify allocation stacks; Go's heap profile does not directly print the GC root path. Root ownership is inferred from source plus the purge control and retained coroutine stacks.
- Three trials provide a clear signal and bounded local variability, not a cross-platform performance characterization.
- Forced-GC CPU is a controlled comparative diagnostic, not an estimate of production latency or total application CPU.
- The experiment covers stopped workers with incomplete cached workflows; it does not quantify every creation, replay, start-failure, eviction, or multi-worker lifecycle path.

No production SDK behavior or fix was implemented. No push, GitHub issue/PR creation or edit, or other external write was performed.
