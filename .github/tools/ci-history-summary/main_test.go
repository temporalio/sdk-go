package main

import (
	"strings"
	"testing"
	"time"
)

func TestBuildJobSummariesCollapsesFailuresByRunAndName(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	jobs := []workflowJob{
		{
			Name:        "unit-test (ubuntu-latest, stable)",
			Conclusion:  "success",
			RunID:       1,
			CompletedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			Name:        "unit-test (ubuntu-latest, stable)",
			Conclusion:  "failure",
			RunID:       1,
			CompletedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			Name:        "unit-test (ubuntu-latest, stable)",
			Conclusion:  "success",
			RunID:       2,
			CompletedAt: now.AddDate(0, 0, -8).Format(time.RFC3339),
		},
		{
			Name:        "unit-test (ubuntu-latest, oldstable)",
			Conclusion:  "skipped",
			RunID:       3,
			CompletedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
	}

	rows := buildJobSummaries(jobs, now)
	if len(rows) != 1 {
		t.Fatalf("expected one reportable row, got %d", len(rows))
	}

	row := rows[0]
	if row.SevenRuns != 1 || row.SevenFailures != 1 || row.SevenPassRate != "0%" {
		t.Fatalf("unexpected 7d stats: %+v", row)
	}
	if row.ThirtyRuns != 2 || row.ThirtyFailures != 1 || row.ThirtyPassRate != "50%" {
		t.Fatalf("unexpected 30d stats: %+v", row)
	}
}

func TestRenderRecentFailedJobsGroupsAndLimits(t *testing.T) {
	cfg := config{
		repository: "temporalio/sdk-go",
		serverURL:  "https://github.com",
		maxFailed:  1,
	}
	jobs := []workflowJob{
		{
			Name:        "features-test / run-tests",
			Conclusion:  "failure",
			RunID:       10,
			RunSHA:      "abcdef123456",
			HTMLURL:     "https://example.test/job",
			CompletedAt: "2026-05-11T10:00:00Z",
		},
		{
			Name:        "unit-test (ubuntu-latest, stable)",
			Conclusion:  "failure",
			RunID:       11,
			RunSHA:      "123456abcdef",
			CompletedAt: "2026-05-10T10:00:00Z",
		},
	}

	rendered := renderRecentFailedJobs(cfg, jobs)
	if !strings.Contains(rendered, "### features-test") {
		t.Fatalf("expected normalized features-test heading, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "unit-test") {
		t.Fatalf("expected maxFailed limit to suppress second failure, got:\n%s", rendered)
	}
}
