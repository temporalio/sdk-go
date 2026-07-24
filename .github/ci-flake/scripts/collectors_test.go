package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSelectRuns(t *testing.T) {
	repository := struct {
		FullName string `json:"full_name"`
	}{FullName: "temporalio/sdk-go"}
	base := apiRun{
		Name:       "CI",
		Status:     "completed",
		CreatedAt:  "2026-07-24T10:00:00Z",
		Repository: repository,
	}
	sameRepository := &struct {
		FullName string `json:"full_name"`
	}{FullName: "temporalio/sdk-go"}
	forkRepository := &struct {
		FullName string `json:"full_name"`
	}{FullName: "someone/fork"}
	runs := []apiRun{
		base,
		base,
		base,
		base,
		base,
	}
	runs[0].ID = 1
	runs[0].Event = "push"
	runs[0].HeadRepository = sameRepository
	runs[1].ID = 2
	runs[1].Event = "pull_request"
	runs[1].HeadRepository = sameRepository
	runs[2].ID = 3
	runs[2].Event = "workflow_dispatch"
	runs[2].HeadRepository = sameRepository
	runs[3].ID = 4
	runs[3].Event = "pull_request"
	runs[3].HeadRepository = forkRepository
	runs[4].ID = 5
	runs[4].Name = "CI flake investigator"
	runs[4].Event = "push"
	runs[4].HeadRepository = sameRepository
	selected, limited := selectRuns(
		runs,
		"CI",
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		10,
	)
	var ids []int64
	for _, run := range selected {
		ids = append(ids, run.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1, 2, 4}) {
		t.Fatalf("unexpected selected runs: %v", ids)
	}
	if limited {
		t.Fatal("selection unexpectedly reported a limit")
	}
}

func TestSelectOutcomeArtifacts(t *testing.T) {
	branch := func(name string) *struct {
		HeadBranch string `json:"head_branch"`
	} {
		return &struct {
			HeadBranch string `json:"head_branch"`
		}{HeadBranch: name}
	}
	artifacts := []apiArtifact{
		{
			ID: 1, Name: "ci-flake-outcome-1", SizeInBytes: 100,
			CreatedAt: "2026-07-24T10:00:00Z", WorkflowRun: branch("main"),
		},
		{
			ID: 2, Name: "other-2", SizeInBytes: 100,
			CreatedAt: "2026-07-24T11:00:00Z", WorkflowRun: branch("main"),
		},
		{
			ID: 3, Name: "ci-flake-outcome-3", Expired: true, SizeInBytes: 100,
			CreatedAt: "2026-07-24T12:00:00Z", WorkflowRun: branch("main"),
		},
		{
			ID: 4, Name: "ci-flake-outcome-4", SizeInBytes: 300000,
			CreatedAt: "2026-07-24T13:00:00Z", WorkflowRun: branch("main"),
		},
		{
			ID: 5, Name: "ci-flake-outcome-5", SizeInBytes: 100,
			CreatedAt: "2026-07-24T14:00:00Z", WorkflowRun: branch("feature"),
		},
	}
	selected := selectOutcomeArtifacts(
		artifacts,
		"main",
		"ci-flake-outcome-",
		60,
	)
	if len(selected) != 1 || selected[0].ID != 1 {
		t.Fatalf("unexpected selected artifacts: %#v", selected)
	}
}

func TestSelectOutcomeEntry(t *testing.T) {
	entry, err := selectOutcomeEntry([]string{"nested/outcome.json"})
	if err != nil || entry != "nested/outcome.json" {
		t.Fatalf("unexpected safe entry result: %q, %v", entry, err)
	}
	if _, err := selectOutcomeEntry([]string{"../outcome.json"}); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if _, err := selectOutcomeEntry(
		[]string{"outcome.json", "nested/outcome.json"},
	); err == nil {
		t.Fatal("expected multiple outcome records to be rejected")
	}
}

func TestFindProtectedFiles(t *testing.T) {
	files := findProtectedFiles([]string{
		"internal/foo.go",
		".github/ci-flake/investigator.md",
		".github/workflows/ci-flake-investigator.yml",
		"AGENTS.md",
	})
	expected := []string{
		".github/ci-flake/investigator.md",
		".github/workflows/ci-flake-investigator.yml",
		"AGENTS.md",
	}
	if !reflect.DeepEqual(files, expected) {
		t.Fatalf("unexpected protected files: %v", files)
	}
}

func TestLinkHumanAuthoredPRRequiresPreservedProvenance(t *testing.T) {
	record := validOutcomeRecord()
	record.ManualFixHandoffProduced = true
	record.NormalizedSignatureHash = pointer(strings.Repeat("a", 64))
	candidates := []dedupCandidate{
		{
			Kind:    "pull-request",
			HTMLURL: "https://github.com/temporalio/sdk-go/pull/123",
			Body: "Investigator workflow: " + record.WorkflowRunURL +
				"\nNormalized signature: " + *record.NormalizedSignatureHash,
		},
	}
	linkHumanAuthoredPR(&record, candidates)
	if record.LinkedHumanPRURL == nil ||
		*record.LinkedHumanPRURL != candidates[0].HTMLURL {
		t.Fatalf("manual PR was not linked: %#v", record)
	}

	withoutRunURL := validOutcomeRecord()
	withoutRunURL.ManualFixHandoffProduced = true
	withoutRunURL.NormalizedSignatureHash = pointer(strings.Repeat("a", 64))
	candidates[0].Body = "Normalized signature: " + *withoutRunURL.NormalizedSignatureHash
	linkHumanAuthoredPR(&withoutRunURL, candidates)
	if withoutRunURL.LinkedHumanPRURL != nil {
		t.Fatalf("PR without full provenance was linked: %#v", withoutRunURL)
	}
}
