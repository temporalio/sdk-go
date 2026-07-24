# CI flake investigator

This directory contains the report-only pilot for automated CI flake
investigation. The workflow runs every day at 08:00 America/New_York and can be
started manually after it is present on the default branch.

## Pilot behavior

The workflow:

- collects a bounded snapshot of recent `CI` workflow runs and failed-job logs;
- runs Codex without GitHub credentials or network access;
- publishes a validated findings summary for every run;
- uploads a minimal outcome record used for last-30-run statistics;
- produces an inspectable manual fix handoff when a high-confidence tested
  patch meets every eligibility gate.

It cannot push a branch or create, edit, approve, or merge a pull request.
Workflow success reports automation health, not whether a patch or PR resulted.

The files in `scripts/` form a dependency-free Go command used for bounded
evidence collection, patch capture, result validation, safe report rendering,
and rolling statistics. The workflow builds the command before invoking Codex
and uses that prebuilt binary for all post-investigation processing.

## Setup

1. Add an `OPENAI_API_KEY` repository secret. During the pilot this may be a
   narrowly scoped developer project key if organization policy permits.
2. Leave `CI_FLAKE_AUTOMATION_ENABLED` unset or set it to `true`.
3. Merge the workflow to the default branch.
4. Run **CI flake investigator** manually with a 24-, 72-, or 168-hour
   lookback, then inspect the run summary and artifacts.

Set the repository or organization Actions variable
`CI_FLAKE_AUTOMATION_ENABLED=false` to disable investigation jobs immediately.

## Outputs

- `ci-flake-outcome-<run-id>-<attempt>`: aggregate metadata only, retained for
  90 days.
- `ci-flake-report-<run-id>-<attempt>`: validated detailed assessment, retained
  for 30 days.
- `ci-flake-manual-fix-<run-id>-<attempt>`: candidate patch and manual PR
  guidance, present only for a patch-ready result and retained for 30 days.

When a manual handoff becomes a human-authored pull request, preserve the
automation provenance in `draft-pr.md`. Later runs match both the workflow URL
and normalized signature to associate that PR with the handoff statistics.

See [plan.md](plan.md) for the staged publisher and fast-path rollout.
