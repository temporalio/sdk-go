# Reproducer: terminated workflows are retained by the sticky cache until LRU capacity

Externally terminated workflow executions are never evicted from the worker's
sticky cache. The worker keeps the full cached execution context, including the
parked workflow goroutine, until `defaultStickyCacheSize` (10000) newer runs
push the dead one out. The server never dispatches a workflow task on
termination, so the worker has no way to learn the run is closed.

## Run

```bash
temporal server start-dev --headless --port 7244
go run . 300 1024        # 300 runs, 1 MB ballast each, started + terminated
```

Observed (sdk v1.48.0, darwin/arm64):

```
baseline: heap 6.2 MB, 25 goroutines
after 300 terminated (1024KB ballast): heap 311.2 MB, 327 goroutines
```

Every terminated run retains its ~1 MB workflow state and one parked goroutine.
Normally completed runs are freed immediately (the worker participates in the
completion), so this is specific to closures the worker never observes:
terminate and server-side workflow execution timeout.

## Probe: nothing reaches the worker on terminate

```bash
go run ./probe
```

```
before terminate: 2 predicate wakeups, 28 goroutines
terminated; waiting 10s for anything to reach the worker...
after terminate: 2 predicate wakeups (delta 0), 28 goroutines (delta 0)
server-side history of the terminated run:
  WorkflowExecutionStarted
  WorkflowTaskScheduled
  WorkflowTaskStarted
  WorkflowTaskCompleted
  WorkflowExecutionTerminated
```

`WorkflowExecutionTerminated` is recorded in history, but no workflow task
delivers it: the `workflow.Await` predicate is never re-evaluated and the
workflow goroutine stays parked.

Related: temporalio/features#573 (fine control for workflow cache eviction),
temporalio/sdk-rust#1135 (rejected fix attempt for the same behavior in Core),
temporalio/sdk-php#635 (the PHP report this was reduced from; PHP workers hit
it hardest since one process holds the whole cache under `memory_limit`).
