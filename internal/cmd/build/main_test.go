package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestOutputFlagsDefaultToFailures(t *testing.T) {
	flags := addTestOutputFlags(flag.NewFlagSet("test", flag.ContinueOnError))
	if flags.consoleOutput != testConsoleOutputFailures {
		t.Fatalf("expected default console output %q, got %q", testConsoleOutputFailures, flags.consoleOutput)
	}
}

func TestPrepareTestOutput(t *testing.T) {
	rootDir := t.TempDir()
	b := &builder{rootDir: rootDir}

	output, err := b.prepareTestOutput(testOutputFlags{
		logDir:        defaultTestLogDir,
		consoleOutput: testConsoleOutputFailures,
	}, "unit-test.log")
	if err != nil {
		t.Fatal(err)
	}
	expectedPath := filepath.Join(rootDir, ".build", "test-logs", "unit-test.log")
	if output.logPath != expectedPath {
		t.Fatalf("expected log path %q, got %q", expectedPath, output.logPath)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected prepared log file: %v", err)
	}

	output, err = b.prepareTestOutput(testOutputFlags{
		logDir:        "artifacts/test-logs",
		consoleOutput: testConsoleOutputFull,
	}, "integration-test.log")
	if err != nil {
		t.Fatal(err)
	}
	expectedPath = filepath.Join(rootDir, "artifacts", "test-logs", "integration-test.log")
	if output.logPath != expectedPath {
		t.Fatalf("expected overridden log path %q, got %q", expectedPath, output.logPath)
	}

	_, err = b.prepareTestOutput(testOutputFlags{
		logDir:        defaultTestLogDir,
		consoleOutput: "invalid",
	}, "unit-test.log")
	if err == nil || !strings.Contains(err.Error(), `must be "full" or "failures"`) {
		t.Fatalf("expected invalid console output error, got %v", err)
	}
}

func TestRunTestCmdFailureOutput(t *testing.T) {
	rootDir := t.TempDir()
	b := &builder{rootDir: rootDir}
	output, err := b.prepareTestOutput(testOutputFlags{
		logDir:        defaultTestLogDir,
		consoleOutput: testConsoleOutputFailures,
	}, "unit-test.log")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	output.stdout = &stdout
	output.stderr = &stderr

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestCmdHelperProcess$")
	cmd.Env = append(os.Environ(), "TEMPORAL_RUN_TEST_CMD_HELPER=fail")
	if err := b.runTestCmd(cmd, output); err == nil {
		t.Fatal("expected command to fail")
	}

	logData, err := os.ReadFile(output.logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"=== RUN   TestFailed", "main_test.go:10: boom"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("full log missing %q:\n%s", want, logData)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("failure output missing %q:\n%s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected stdout to be suppressed, got:\n%s", stdout.String())
	}
}

func TestRunTestCmdFullOutput(t *testing.T) {
	rootDir := t.TempDir()
	b := &builder{rootDir: rootDir}
	output, err := b.prepareTestOutput(testOutputFlags{
		logDir:        defaultTestLogDir,
		consoleOutput: testConsoleOutputFull,
	}, "unit-test.log")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	output.stdout = &stdout
	output.stderr = &stderr

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestCmdHelperProcess$")
	cmd.Env = append(os.Environ(), "TEMPORAL_RUN_TEST_CMD_HELPER=pass")
	if err := b.runTestCmd(cmd, output); err != nil {
		t.Fatal(err)
	}

	logData, err := os.ReadFile(output.logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stdout output", "stderr output"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("full log missing %q:\n%s", want, logData)
		}
	}
	if !strings.Contains(stdout.String(), "stdout output") {
		t.Fatalf("stdout output was not streamed:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr output") {
		t.Fatalf("stderr output was not streamed:\n%s", stderr.String())
	}
}

func TestWriteTestFailureSnippetsFallsBackToCommandOutput(t *testing.T) {
	var output bytes.Buffer
	err := writeTestFailureSnippets(
		&output,
		"# example.com/pkg\n./main.go:10: undefined: missing\nFAIL\n",
		".build/test-logs/unit-test.log",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"No individual failed test was identified",
		"./main.go:10: undefined: missing",
		".build/test-logs/unit-test.log",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("fallback output missing %q:\n%s", want, output.String())
		}
	}
}

func TestWriteTestFailureSnippetsListsAllFailuresWhenDetailsAreOmitted(t *testing.T) {
	var testOutput strings.Builder
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&testOutput, "=== RUN   TestFailed%d\n", i)
		testOutput.WriteString(strings.Repeat("x", 20*1024))
		fmt.Fprintf(&testOutput, "\n--- FAIL: TestFailed%d (0.00s)\n", i)
	}
	testOutput.WriteString("FAIL\nFAIL\texample.com/pkg\t0.001s\n")

	var snippets bytes.Buffer
	err := writeTestFailureSnippets(
		&snippets,
		testOutput.String(),
		".build/test-logs/unit-test.log",
	)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		want := fmt.Sprintf("- example.com/pkg / TestFailed%d", i)
		if !strings.Contains(snippets.String(), want) {
			t.Fatalf("failure list missing %q", want)
		}
	}
	for _, want := range []string{
		"Failure details (up to 16 KiB per test and 64 KiB total)",
		"... (truncated; see full test log) ...",
		"omitted details for 2 additional failed tests after reaching the 64 KiB total console limit",
		"all failed tests are listed above",
	} {
		if !strings.Contains(snippets.String(), want) {
			t.Fatalf("failure output missing %q:\n%s", want, snippets.String())
		}
	}
}

func TestRunTestCmdHelperProcess(t *testing.T) {
	switch os.Getenv("TEMPORAL_RUN_TEST_CMD_HELPER") {
	case "":
		return
	case "pass":
		fmt.Fprintln(os.Stdout, "stdout output")
		fmt.Fprintln(os.Stderr, "stderr output")
	case "fail":
		fmt.Fprintln(os.Stdout, "=== RUN   TestFailed")
		fmt.Fprintln(os.Stdout, "--- FAIL: TestFailed (0.00s)")
		fmt.Fprintln(os.Stdout, "    main_test.go:10: boom")
		fmt.Fprintln(os.Stdout, "FAIL")
		fmt.Fprintln(os.Stdout, "FAIL\texample.com/pkg\t0.001s")
		os.Exit(1)
	default:
		t.Fatalf("unexpected helper mode")
	}
}

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

func TestFilterParentFailureRowsUsesPackage(t *testing.T) {
	rows := filterParentFailureRows([]testFailureSummaryRow{
		{Package: "example.com/a", Test: "TestSuite"},
		{Package: "example.com/a", Test: "TestSuite/TestSub"},
		{Package: "example.com/b", Test: "TestSuite"},
	})

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %#v", len(rows), rows)
	}
	if rows[0].Package != "example.com/a" || rows[0].Test != "TestSuite/TestSub" {
		t.Fatalf("expected package a subtest row first, got %#v", rows[0])
	}
	if rows[1].Package != "example.com/b" || rows[1].Test != "TestSuite" {
		t.Fatalf("expected package b parent row to be preserved, got %#v", rows[1])
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
