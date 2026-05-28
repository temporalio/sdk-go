package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTestFailures(t *testing.T) {
	output := strings.Join([]string{
		"=== RUN   TestWorkflowSuite",
		"=== RUN   TestWorkflowSuite/TestChildWorkflow",
		"2026/05/28 19:18:39 INFO  Started Worker",
		"    workflow_test.go:42: expected success",
		"2026/05/28 19:18:50 INFO  Stopped Worker",
		"--- FAIL: TestWorkflowSuite (0.01s)",
		"    --- FAIL: TestWorkflowSuite/TestChildWorkflow (0.01s)",
		"        workflow_test.go:42: expected success",
		"=== RUN   TestWorkflowSuite/TestNextWorkflow",
		"2026/05/28 19:19:01 INFO  Next test output",
		"--- PASS: TestWorkflowSuite/TestNextWorkflow (0.01s)",
		"FAIL",
		"FAIL\tgo.temporal.io/sdk/internal\t0.123s",
		"",
	}, "\n")

	rows := parseTestFailures(output)
	if len(rows) != 1 {
		t.Fatalf("expected 1 failure row, got %d: %#v", len(rows), rows)
	}
	for _, row := range rows {
		if row.Package != "go.temporal.io/sdk/internal" {
			t.Fatalf("expected package to be filled from FAIL line, got %#v", row)
		}
	}
	if rows[0].Test != "TestWorkflowSuite/TestChildWorkflow" {
		t.Fatalf("unexpected test: %q", rows[0].Test)
	}
	if !strings.Contains(rows[0].Details, "expected success") {
		t.Fatalf("expected detail block to include failure message, got %q", rows[0].Details)
	}
	for _, want := range []string{
		"=== RUN   TestWorkflowSuite/TestChildWorkflow",
		"Started Worker",
		"Stopped Worker",
	} {
		if !strings.Contains(rows[0].Details, want) {
			t.Fatalf("expected detail block to include %q, got %q", want, rows[0].Details)
		}
	}
	if strings.Contains(rows[0].Details, "Next test output") {
		t.Fatalf("expected detail block to stop before next test, got %q", rows[0].Details)
	}
}

func TestRenderTestFailureSummaryEscapesHTML(t *testing.T) {
	summary := renderTestFailureSummary([]testFailureSummaryRow{
		{
			Test:    "Test<Bad>",
			Package: "go.temporal.io/sdk/test",
			Details: "got <value> & failed",
		},
	})

	for _, want := range []string{
		"## Test failures",
		"<table>",
		"go.temporal.io/sdk/test / Test&lt;Bad&gt;",
		"got &lt;value&gt; &amp; failed",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "Working directory") {
		t.Fatalf("summary should not include working directory:\n%s", summary)
	}
}

func TestAppendTestFailureSummary(t *testing.T) {
	summaryPath := filepath.Join(t.TempDir(), "summary.md")
	err := appendTestFailureSummary(summaryPath, strings.Join([]string{
		"=== RUN   TestFailed",
		"--- FAIL: TestFailed (0.00s)",
		"    main_test.go:10: boom",
		"FAIL",
		"FAIL\texample.com/pkg\t0.001s",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "example.com/pkg / TestFailed") {
		t.Fatalf("summary not written correctly:\n%s", string(data))
	}
}
