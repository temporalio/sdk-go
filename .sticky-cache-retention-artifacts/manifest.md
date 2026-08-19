# Sticky cache retention artifact manifest

Authoritative final runs for SDK commit `0feb5a6813ab94b6e790260521a94914bb5e3267`:

| Directory | Workload | Trials | Contents |
| --- | --- | ---: | --- |
| `20260819T030426Z` | 32 workflows × 1 MiB state | 3 per scenario | JSON metrics, child logs, `inuse_space` profiles, build-runner log, dev-server log |
| `20260819T030452Z` | 16 workflows × 512 KiB state | 3 per scenario | JSON metrics, child logs, `inuse_space` profiles, build-runner log, dev-server log |

Each directory's `manifest.json` maps scenario/trial pairs to their JSON result and child-process log. Profile names use the same scenario/trial stem.

Measurement commands were run from `internal/cmd/build`:

```bash
TEMPORAL_STICKY_CACHE_PROFILE=1 TEMPORAL_STICKY_CACHE_PROFILE_GC_CYCLES=100 go run . integration-test -dev-server -run '^TestStickyCacheRetentionProfile$'
TEMPORAL_STICKY_CACHE_PROFILE=1 TEMPORAL_STICKY_CACHE_PROFILE_WORKFLOWS=16 TEMPORAL_STICKY_CACHE_PROFILE_STATE_BYTES=524288 TEMPORAL_STICKY_CACHE_PROFILE_GC_CYCLES=100 go run . integration-test -dev-server -run '^TestStickyCacheRetentionProfile$'
```

The local command wrapper was `rtk env ...`. The canonical build runner started and stopped a fresh embedded Temporal server for each command. `.build/test-logs/dev-server.log` and `.build/test-logs/integration-test.log` were copied into the corresponding run directory immediately after each run.

Profile commands were run from the repository root:

```bash
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -nodecount=20 -sample_index=inuse_space .sticky-cache-retention-artifacts/20260819T030426Z/unpurged-2.pprof
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -cum -nodecount=20 -sample_index=inuse_space .sticky-cache-retention-artifacts/20260819T030426Z/unpurged-2.pprof
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -nodecount=20 -sample_index=inuse_space -base .sticky-cache-retention-artifacts/20260819T030426Z/purged-2.pprof .sticky-cache-retention-artifacts/20260819T030426Z/unpurged-2.pprof
go run github.com/google/pprof@v0.0.0-20250403155104-27863c87afa6 -top -nodecount=15 -sample_index=inuse_space -base .sticky-cache-retention-artifacts/20260819T030452Z/purged-2.pprof .sticky-cache-retention-artifacts/20260819T030452Z/unpurged-2.pprof
```

`sticky-cache-retention-results.md` is the interpretation and evidence report. Earlier exploratory artifact directories were removed after they exposed harness issues and were superseded by these equal-cycle, correctly timed runs.
