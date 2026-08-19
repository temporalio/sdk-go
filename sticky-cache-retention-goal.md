# Sticky cache retention profiling goal

Paste the contents of the following block into Codex Goal mode from the repository root.

```text
/goal Produce a reproducible, evidence-backed conclusion about whether stopped Temporal Go SDK workers retain sticky workflow-cache state and, if confirmed, quantify the retained heap and GC impact.

Use sticky-cache-retention-investigation.md as the authoritative experiment plan. Read AGENTS.md and the related sticky-cache-gc-issue.md and sticky-cache-lifecycle-design.md before beginning.

Scope:
- Build only the local diagnostic/integration profiling harness needed for this investigation.
- Run cache-disabled, explicitly-purged, and unpurged scenarios in fresh processes.
- Manage the embedded Temporal dev server through the repository's canonical build tooling. Do not depend on a manually started server. Scenarios must use fresh Go processes but may share one goal-owned dev server when that preserves isolation. Capture its logs and stop it during cleanup.
- Capture runtime/metrics values and in-use heap profiles beneath a clearly named directory in this repository.
- Inspect pprof allocation and retaining evidence, not merely aggregate memory values.
- Repeat trials sufficiently to report variability and distinguish the result from process noise.
- If retention is confirmed, measure GC CPU per cycle with a controlled workload.
- Write the final evidence and conclusion to sticky-cache-retention-results.md.
- Update the issue and design drafts locally if the evidence changes their claims.

Constraints and non-goals:
- Do not implement the production fix.
- Do not alter production SDK behavior.
- Local commits containing only this investigation's intentional changes are allowed when useful. Do not push, open or edit a GitHub issue or PR, or write to external services.
- Do not leave a failing test in the normal test suite.
- Keep profiling artifacts and temporary files beneath this repository, per AGENTS.md.
- Preserve unrelated tracked and untracked files.
- A supported negative result is a valid outcome; do not force the evidence to confirm the hypothesis.
- Do not infer GC impact from retained heap alone.

Completion criteria:
1. Each scenario runs in an isolated process with the same workload and records its configuration.
2. At least three usable trials per scenario report live heap, heap objects, scannable heap, and heap-object memory after normalized GC.
3. Representative heap profiles are captured and pprof summaries identify the allocations responsible for differences between scenarios.
4. The report classifies the hypothesis as confirmed, partially confirmed, or disproved and explains the evidence.
5. If confirmed, the report quantifies retained bytes per cached workflow and includes controlled GC CPU-per-cycle measurements.
6. The report proposes a deterministic, non-finalizer-timing-dependent regression test based on the measured retaining path.
7. Relevant focused checks pass, or any failures are documented with evidence showing whether they are environmental or caused by the profiling harness.
8. Temporary executables and disposable scratch data are removed; useful raw measurements and profiles remain with a manifest of commands used.

Final evidence:
- Report all created or modified files, any local commit SHAs, exact commands run, scenario results, pprof findings, variability, conclusion, and residual limitations.
- State explicitly that no production fix, push, or GitHub write was performed.

Do not mark the goal complete after only demonstrating the source-level reference graph, after one profiling run, or after observing a heap-size difference without attribution. Continue through controlled comparisons and a written conclusion. Pause only if required permissions or the embedded dev server remain unavailable after safe retries, and report the exact failing command and evidence.
```

## Iteration notes

- The goal has one outcome: an evidence-backed conclusion, not a fix.
- A disproved hypothesis is an acceptable completed result when supported by the defined controls.
- Profiling code and artifacts are allowed locally; production behavior and external GitHub state are out of scope.
