package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type renderTestPaths struct {
	input   string
	output  string
	history string
}

func writeRenderInputs(
	t *testing.T,
	result investigationResult,
) renderTestPaths {
	t.Helper()
	directory := t.TempDir()
	paths := renderTestPaths{
		input:   filepath.Join(directory, "input"),
		output:  filepath.Join(directory, "output"),
		history: filepath.Join(directory, "history"),
	}
	for _, path := range []string{paths.input, paths.output, paths.history} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := snapshot{
		SchemaVersion:     "1",
		GeneratedAt:       "2026-07-24T12:00:00Z",
		Repository:        "temporalio/sdk-go",
		WorkflowAllowlist: []string{"CI"},
		ExcludedWorkflows: []string{"CI flake investigator"},
		Search: snapshotSearch{
			Cutoff:         "2026-07-21T12:00:00Z",
			LookbackHours:  72,
			IncludedEvents: []string{"push", "pull_request"},
		},
		Limits: snapshotLimits{
			MaxRuns:       80,
			MaxFailedJobs: 20,
			MaxLogBytes:   8388608,
		},
		Coverage:         snapshotCoverage{LimitsReached: []string{}},
		Runs:             []runRecord{},
		DedupCandidates:  []dedupCandidate{},
		RepositoryLabels: []string{},
	}
	if err := writeJSON(filepath.Join(paths.input, "snapshot.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(
		filepath.Join(paths.output, "changed-files.json"),
		changedFiles{
			SchemaVersion:  "1",
			Files:          []string{},
			ProtectedFiles: []string{},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(paths.output, "result.json"), result); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(paths.output, "candidate.patch"),
		[]byte{},
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(
		filepath.Join(paths.history, "collection.json"),
		outcomeCollection{
			SchemaVersion: "1",
			CollectedAt:   "2026-07-24T12:00:00Z",
		},
	); err != nil {
		t.Fatal(err)
	}
	return paths
}

func setRenderEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("CI_FLAKE_BASE_SHA", strings.Repeat("a", 40))
	t.Setenv("CI_FLAKE_CODEX_CONCLUSION", "success")
	t.Setenv("CI_FLAKE_PUBLICATION_MODE", "report-only")
	t.Setenv("CI_FLAKE_RUN_ID", "123")
	t.Setenv("CI_FLAKE_RUN_ATTEMPT", "2")
	t.Setenv(
		"CI_FLAKE_RUN_URL",
		"https://github.com/temporalio/sdk-go/actions/runs/123",
	)
	t.Setenv("CI_FLAKE_TRIGGER_MODE", "manual-reconciliation")
	t.Setenv("CI_FLAKE_RUNBOOK_VERSION", "1")
}

func TestRenderQuietWindowAndStatistics(t *testing.T) {
	paths := writeRenderInputs(t, validInvestigationResult())
	setRenderEnvironment(t)
	if err := renderCurrent(
		filepath.Join(paths.output, "result.json"),
		filepath.Join(paths.input, "snapshot.json"),
		filepath.Join(paths.output, "candidate.patch"),
		filepath.Join(paths.output, "changed-files.json"),
		paths.output,
	); err != nil {
		t.Fatal(err)
	}
	var outcome outcomeRecord
	if err := readJSONStrict(
		filepath.Join(paths.output, "outcome.json"),
		&outcome,
		maxOutcomeRecordBytes,
	); err != nil {
		t.Fatal(err)
	}
	if errors := validateOutcomeRecord(outcome); len(errors) > 0 {
		t.Fatalf("outcome is invalid: %v", errors)
	}
	if outcome.Outcome != "no-failures" ||
		outcome.ManualFixHandoffProduced ||
		outcome.WorkflowRunAttempt != 2 {
		t.Fatalf("unexpected outcome: %#v", outcome)
	}
	summary, err := os.ReadFile(filepath.Join(paths.output, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(summary)), "no failed monitored jobs") {
		t.Fatalf("unexpected summary: %s", summary)
	}
	if err := renderStatistics(
		filepath.Join(paths.output, "outcome.json"),
		paths.history,
		30,
		filepath.Join(paths.output, "statistics.md"),
	); err != nil {
		t.Fatal(err)
	}
	statistics, err := os.ReadFile(filepath.Join(paths.output, "statistics.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"1 of 30 requested runs",
		"Manual reconciliation | 1",
		"| N/A |",
	} {
		if !strings.Contains(string(statistics), expected) {
			t.Fatalf("statistics do not contain %q:\n%s", expected, statistics)
		}
	}
}

func TestRenderWritesFallbackForIncoherentResult(t *testing.T) {
	paths := writeRenderInputs(t, validInvestigationResult())
	var snapshotValue snapshot
	if err := readJSONStrict(
		filepath.Join(paths.input, "snapshot.json"),
		&snapshotValue,
		16*1024*1024,
	); err != nil {
		t.Fatal(err)
	}
	snapshotValue.Coverage.FailedRunsInspected = 1
	if err := writeJSON(
		filepath.Join(paths.input, "snapshot.json"),
		snapshotValue,
	); err != nil {
		t.Fatal(err)
	}
	setRenderEnvironment(t)
	err := renderCurrent(
		filepath.Join(paths.output, "result.json"),
		filepath.Join(paths.input, "snapshot.json"),
		filepath.Join(paths.output, "candidate.patch"),
		filepath.Join(paths.output, "changed-files.json"),
		paths.output,
	)
	if err == nil || !strings.Contains(err.Error(), "no-failures conflicts") {
		t.Fatalf("expected coherence error, got %v", err)
	}
	var outcome outcomeRecord
	if err := readJSONStrict(
		filepath.Join(paths.output, "outcome.json"),
		&outcome,
		maxOutcomeRecordBytes,
	); err != nil {
		t.Fatal(err)
	}
	if outcome.Outcome != "error" || outcome.PayloadValid || !outcome.FallbackUsed {
		t.Fatalf("unexpected fallback outcome: %#v", outcome)
	}
	summary, err := os.ReadFile(filepath.Join(paths.output, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "Automation error") {
		t.Fatalf("unexpected fallback summary: %s", summary)
	}
}
