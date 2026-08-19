# Sticky workflow cache retention investigation

## Status

Completed. The hypothesis was confirmed. See `sticky-cache-retention-results.md` for raw-run locations, repeated scenario measurements, heap-profile attribution, controlled GC results, exact commands, and limitations. The opt-in harness remains in `test/sticky_cache_retention_profile_test.go` for before/after fix validation.

## Purpose

Test the suspected stopped-worker retention path before filing a definitive bug or designing an implementation around it.

The investigation must answer three separate questions:

1. **Lifecycle:** Does stopping the last worker synchronously release its ownership of the sticky cache?
2. **Retention:** After application references are dropped, does the global cache keep workflow execution state live?
3. **Impact:** If state is retained, how many bytes and objects remain, and does that materially increase GC work?

A code-level reference cycle answers none of these quantitatively. A heap-size difference alone also does not prove the proposed retaining path. The experiment needs controls, heap profiles, and repeatable measurements.

## Recommended Evidence Order

### Phase 1: deterministic lifecycle probe

Add a focused internal test or temporary diagnostic that observes cache ownership immediately before and after `AggregatedWorker.Stop`.

The probe should:

1. create a worker cache lease;
2. place a representative workflow execution context into the shared LRU;
3. stop the owning worker through its normal lifecycle;
4. assert the shared owner count and cache contents immediately, without calling `runtime.GC`.

This establishes whether shutdown releases anything deterministically. It does not by itself prove indefinite retention or user impact.

Do not begin with a test that waits for a finalizer not to run. Proving a negative with a timeout is slow and flaky. The existing `TestCreateAndFree` also is not sufficient because it directly calls the private `close` method rather than exercising worker lifecycle.

### Phase 2: isolated end-to-end heap experiment

Run each scenario in a fresh process. The sticky cache and its owner count are package globals, so running scenarios sequentially in one test process would make the controls dependent on ordering and prior finalizers.

Use this matrix:

| Scenario | Sticky cache | After worker stop | Purpose |
| --- | ---: | --- | --- |
| `disabled` | `0` | no purge | Lower-bound control |
| `purged` | nonzero | call `worker.PurgeStickyWorkflowCache()` | Positive-release control |
| `unpurged` | nonzero | no purge | Suspected retention |

The `purged` and `unpurged` scenarios must use the same workflow count, workflow state size, worker options, server version, and measurement sequence.

### Phase 3: GC cost

Only measure GC cost after Phase 2 confirms retained state and identifies its retaining path. Retained heap and GC CPU are related but are not interchangeable results.

## Workload

Use the repository's integration-test harness and embedded dev server. A workflow should allocate deterministic state and then remain blocked after completing its first workflow task.

Conceptually:

```go
func stickyRetentionWorkflow(ctx workflow.Context, stateSize int) error {
    state := make([]byte, stateSize)
    state[len(state)-1] = 1
    return workflow.Await(ctx, func() bool {
        return state[len(state)-1] == 2
    })
}
```

The closure must read the state so the compiler and workflow runtime cannot discard it. The workflow must not use nondeterministic APIs.

Suggested local profiling defaults:

- 32 workflows;
- 512 KiB or 1 MiB of state per workflow;
- cache capacity greater than the workflow count;
- eager workflow start disabled to reduce alternate ownership paths.

These values should create a clear signal without making the experiment excessively large. They are profiling defaults, not proposed CI-test sizes.

Wait on an observed condition instead of sleeping. The existing capturing metrics handler exposes the `temporal_sticky_cache_size` gauge, and worker-heartbeat tests already demonstrate waiting until sticky cache state is reported. Continue only after the cache size reaches the expected workflow count.

## Process Isolation

Implement the eventual probe as a top-level test or small diagnostic command with a scenario flag, then invoke it once per process:

```text
scenario=disabled
scenario=purged
scenario=unpurged
```

Each process should:

1. configure cache size before creating any worker;
2. connect to the dev server;
3. start workflows and wait for the expected cache-size gauge;
4. stop the worker and set worker/run references to nil;
5. close and nil the client;
6. apply the scenario-specific purge;
7. normalize and record memory state;
8. write the heap profile beneath a task-specific directory in the repository;
9. terminate the server-side workflows after measurement if cleanup is needed.

The driver should randomize scenario execution order across repeated trials, or run each scenario enough times that process startup noise can be reported.

## Memory Measurements

After shutdown, drop application references and force two or more GCs for diagnostic normalization. Forced GC is acceptable in this profiling harness but must not become part of the product behavior or regression-test success condition.

Read at least these `runtime/metrics` values:

- `/gc/heap/live:bytes`
- `/gc/heap/objects:objects`
- `/gc/scan/heap:bytes`
- `/memory/classes/heap/objects:bytes`
- `/gc/cycles/forced:gc-cycles`

Also record:

- scenario name;
- workflow count and state size;
- observed sticky cache size before shutdown;
- Go version, SDK commit, OS, architecture, and `GOMAXPROCS`;
- `GOGC`, `GOMEMLIMIT`, and relevant `GODEBUG` settings;
- elapsed time between shutdown and snapshot.

Write an `inuse_space` heap profile with `runtime/pprof`. Analyze both aggregate retained bytes and allocation stacks:

```bash
go tool pprof -top <test-binary> <heap-profile>
go tool pprof -top -cum <test-binary> <heap-profile>
```

The profile should show whether retained memory is attributable to workflow state and reachable SDK structures, rather than the test server, gRPC buffers, metrics capture, workflow handles, or test bookkeeping.

## GC Cost Measurement

If the unpurged scenario retains materially more scannable heap, measure GC cost in a second, controlled phase.

Recommended approach:

1. reach the post-shutdown measurement state;
2. record `/cpu/classes/gc/total:cpu-seconds` and `/gc/cycles/total:gc-cycles`;
3. run a fixed allocation workload that triggers a known number of GC cycles;
4. record the metric deltas;
5. repeat the process in fresh processes for all three scenarios.

Report GC CPU per completed cycle and total wall time. Use medians and ranges across multiple trials. Do not use a single forced-GC duration as the headline result; scheduler noise and process startup can dominate it.

An optional CPU profile can corroborate the runtime metrics, but it is secondary to the heap profile and controlled metric deltas.

## Avoiding False Positives

- Run scenarios in separate processes because the cache is global.
- Do not retain `worker.Worker`, `client.Client`, workflow-run handles, closures, or metric objects longer than necessary.
- Capture a pre-workload baseline in every process.
- Keep server-side workflow executions separate from local heap ownership; terminate them only after the post-shutdown snapshot.
- Confirm all workflow tasks have completed and entered the sticky cache before stopping the worker.
- Ensure shutdown has finished rather than merely requesting shutdown.
- Keep profiling artifacts out of system temporary directories; use a named directory beneath the repository.
- Repeat trials to distinguish retained state from allocator and runtime noise.
- Inspect retaining allocation stacks. Do not infer a `WorkerCache` root solely from `HeapAlloc`.

## Interpretation

### Confirmed

Treat the hypothesis as confirmed if all of the following hold:

- the unpurged process retains substantially more live and scannable heap than both controls;
- retained bytes scale with workflow count or workflow state size;
- the heap profile attributes the difference to cached workflow execution state or its event-handler/coroutine state;
- explicit purge removes most of that difference;
- deterministic lifecycle inspection shows no normal shutdown release.

### Partially confirmed

Use this result if the ownership count remains live but heap impact is small, state is eventually released through another deterministic path, or the profile identifies retention but not the proposed `WorkerCache` back-reference.

The fix should target the measured owner, not the initially suspected graph.

### Disproved

Treat the hypothesis as disproved if the unpurged scenario converges with the controls after references are dropped and GC is normalized, or if the heap profile identifies only test/harness references.

Document the evidence and do not implement the provisional lease redesign.

## Regression Test After Confirmation

The permanent regression test should validate deterministic lifecycle state, not heap size or finalizer timing:

- populate the cache through representative workflow-task handling;
- stop the owning worker normally;
- assert that its ownership lease is released synchronously;
- verify another live worker preserves the shared cache;
- verify the final release detaches and cleans the cache;
- use no fixed sleeps, `runtime.GC`, or finalizer polling.

Keep heap profiling as a developer diagnostic or benchmark because absolute heap assertions are too sensitive for CI.

## Deliverables Before Solution Work

- Raw metric output for each scenario and trial.
- Heap profiles and exact commands used to capture them.
- A short table of median retained live bytes, scannable heap, and heap objects.
- `pprof` top and cumulative summaries identifying the retaining stack.
- GC CPU-per-cycle results if retention is confirmed.
- A conclusion of confirmed, partially confirmed, or disproved.
- A minimal deterministic failing test plan derived from the measured ownership path.

Only after these deliverables exist should `sticky-cache-lifecycle-design.md` be treated as an implementation proposal.
