package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validInvestigationResult() investigationResult {
	return investigationResult{
		SchemaVersion: "1",
		Outcome:       "no-failures",
		Headline:      "No monitored CI failures were found.",
		Confidence:    "high",
		Decision: decision{
			ReasonCode:  "no-failures",
			Explanation: "The bounded snapshot contained no failed CI runs.",
		},
		PrimarySignature: signature{},
		Findings:         []finding{},
		RecommendedFix: recommendedFix{
			Summary:         "",
			AffectedFiles:   []string{},
			AffectedSymbols: []string{},
		},
		MissingEvidence: []string{},
		NextExperiment: nextExperiment{
			Summary:  "Continue daily reconciliation.",
			Commands: []string{},
		},
		Verification: []verification{},
		ManualFix: manualFix{
			Labels:        []string{},
			ReviewerNotes: []string{},
		},
	}
}

func validOutcomeRecord() outcomeRecord {
	return outcomeRecord{
		SchemaVersion:            "1",
		WorkflowRunID:            100,
		WorkflowRunAttempt:       1,
		WorkflowRunURL:           "https://github.com/temporalio/sdk-go/actions/runs/100",
		CreatedAt:                "2026-07-24T12:00:00Z",
		TriggerMode:              "daily-reconciliation",
		PublicationMode:          "report-only",
		Outcome:                  "no-failures",
		PROpened:                 false,
		PRURL:                    nil,
		ManualFixHandoffProduced: false,
		NormalizedSignatureHash:  nil,
		PayloadValid:             true,
		FallbackUsed:             false,
		LinkedHumanPRURL:         nil,
	}
}

func TestValidateInvestigationResult(t *testing.T) {
	if errors := validateInvestigationResult(validInvestigationResult()); len(errors) > 0 {
		t.Fatalf("valid result rejected: %v", errors)
	}
}

func TestValidateInvestigationResultRejectsLongPRBody(t *testing.T) {
	result := validInvestigationResult()
	result.Outcome = "patch-ready"
	result.ManualFix.Eligible = true
	result.ManualFix.PRBody = pointer(strings.Repeat("a", 3001))
	errors := validateInvestigationResult(result)
	if len(errors) == 0 ||
		!strings.Contains(strings.Join(errors, "; "), "manual_fix.pr_body exceeds 3000") {
		t.Fatalf("expected long PR body to be rejected: %v", errors)
	}
}

func TestValidateInvestigationResultRejectsPRBodyOverWordLimit(t *testing.T) {
	result := validInvestigationResult()
	result.Outcome = "patch-ready"
	result.ManualFix.Eligible = true
	result.ManualFix.PRBody = pointer(strings.Repeat("word ", 301))
	errors := validateInvestigationResult(result)
	if len(errors) == 0 ||
		!strings.Contains(strings.Join(errors, "; "), "manual_fix.pr_body exceeds 300 words") {
		t.Fatalf("expected PR body over word limit to be rejected: %v", errors)
	}
}

func TestReadJSONStrictRejectsUnexpectedAndTrailingFields(t *testing.T) {
	directory := t.TempDir()
	unexpected := filepath.Join(directory, "unexpected.json")
	if err := os.WriteFile(
		unexpected,
		[]byte(`{"schema_version":"1","unexpected":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var result investigationResult
	if err := readJSONStrict(unexpected, &result, 1024); err == nil {
		t.Fatal("expected unexpected JSON field to be rejected")
	}

	trailing := filepath.Join(directory, "trailing.json")
	if err := os.WriteFile(trailing, []byte(`{} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readJSONStrict(trailing, &result, 1024); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestSanitizeText(t *testing.T) {
	sanitized := sanitizeText(
		"<b>![x](https://example.com)\x00 sk-exampleSecret123456</b>",
		1600,
	)
	for _, unsafe := range []string{"<b>", "![", "sk-exampleSecret", "example.com"} {
		if strings.Contains(sanitized, unsafe) {
			t.Fatalf("sanitized text still contains %q: %q", unsafe, sanitized)
		}
	}
	for _, expected := range []string{"&lt;b&gt;", "[REDACTED]", "external URL removed"} {
		if !strings.Contains(sanitized, expected) {
			t.Fatalf("sanitized text does not contain %q: %q", expected, sanitized)
		}
	}
}

func TestValidateOutcomeRecord(t *testing.T) {
	record := validOutcomeRecord()
	if errors := validateOutcomeRecord(record); len(errors) > 0 {
		t.Fatalf("valid outcome rejected: %v", errors)
	}
	record.WorkflowRunURL = "https://example.com/run/100"
	if errors := validateOutcomeRecord(record); len(errors) == 0 {
		t.Fatal("expected a non-GitHub workflow URL to be rejected")
	}
}

func TestUniqueLatestRecordsUsesLatestAttempt(t *testing.T) {
	first := validOutcomeRecord()
	first.WorkflowRunID = 1
	first.CreatedAt = "2026-07-21T12:00:00Z"
	secondOld := validOutcomeRecord()
	secondOld.WorkflowRunID = 2
	secondOld.WorkflowRunAttempt = 1
	secondOld.CreatedAt = "2026-07-22T12:00:00Z"
	secondNew := secondOld
	secondNew.WorkflowRunAttempt = 2
	secondNew.CreatedAt = "2026-07-23T12:00:00Z"
	records := uniqueLatestRecords(
		[]outcomeRecord{first, secondOld, secondNew},
		30,
	)
	if len(records) != 2 ||
		records[0].WorkflowRunID != 2 ||
		records[0].WorkflowRunAttempt != 2 ||
		records[1].WorkflowRunID != 1 {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestSummarizeRecords(t *testing.T) {
	reportOnly := validOutcomeRecord()
	prototype := validOutcomeRecord()
	prototype.WorkflowRunID = 2
	prototype.PublicationMode = "github-token-prototype"
	opened := validOutcomeRecord()
	opened.WorkflowRunID = 3
	opened.PublicationMode = "github-app"
	opened.Outcome = "draft-pr-opened"
	opened.PROpened = true
	opened.PRURL = pointer("https://github.com/temporalio/sdk-go/pull/3")
	noPR := validOutcomeRecord()
	noPR.WorkflowRunID = 4
	noPR.PublicationMode = "github-app"
	noPR.Outcome = "no-credible-flake"
	failed := validOutcomeRecord()
	failed.WorkflowRunID = 5
	failed.PublicationMode = "github-app"
	failed.Outcome = "error"

	summary := summarizeRecords(
		[]outcomeRecord{reportOnly, prototype, opened, noPR, failed},
	)
	if summary.PublishEnabledCompleted != 2 ||
		summary.PRs != 1 ||
		summary.NoPR != 1 ||
		summary.ReportOnly != 1 ||
		summary.Prototype != 1 ||
		summary.PartialOrError != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if got := formatPercent(summary.PRs, summary.PublishEnabledCompleted); got != "50.0%" {
		t.Fatalf("unexpected percentage: %s", got)
	}
	if got := formatPercent(0, 0); got != "N/A" {
		t.Fatalf("unexpected empty percentage: %s", got)
	}
}
