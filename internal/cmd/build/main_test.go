package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	expectedJSONPath := filepath.Join(rootDir, ".build", "test-logs", "unit-test.json")
	if output.jsonLogPath != expectedJSONPath {
		t.Fatalf("expected JSON log path %q, got %q", expectedJSONPath, output.jsonLogPath)
	}
	if _, err := os.Stat(expectedJSONPath); err != nil {
		t.Fatalf("expected prepared JSON log file: %v", err)
	}

	output, err = b.prepareTestOutput(testOutputFlags{
		logDir:        "artifacts/test-logs",
		consoleOutput: testConsoleOutputFull,
	}, "go-test.log")
	if err != nil {
		t.Fatal(err)
	}
	expectedPath = filepath.Join(rootDir, "artifacts", "test-logs", "go-test.log")
	if output.logPath != expectedPath {
		t.Fatalf("expected overridden log path %q, got %q", expectedPath, output.logPath)
	}
	expectedJSONPath = filepath.Join(rootDir, "artifacts", "test-logs", "go-test.json")
	if output.jsonLogPath != expectedJSONPath {
		t.Fatalf("expected overridden JSON log path %q, got %q", expectedJSONPath, output.jsonLogPath)
	}

	_, err = b.prepareTestOutput(testOutputFlags{
		logDir:        defaultTestLogDir,
		consoleOutput: "invalid",
	}, "unit-test.log")
	if err == nil || !strings.Contains(err.Error(), `must be "full" or "failures"`) {
		t.Fatalf("expected invalid console output error, got %v", err)
	}
}

func TestTestOutputWriters(t *testing.T) {
	for _, mode := range []string{testConsoleOutputFailures, testConsoleOutputFull} {
		t.Run(mode, func(t *testing.T) {
			var logOutput, stdout, stderr bytes.Buffer
			output := testOutput{
				consoleOutput: mode,
				stdout:        &stdout,
				stderr:        &stderr,
			}
			stdoutWriter, stderrWriter := output.writers(&logOutput, nil)
			fmt.Fprint(stdoutWriter, "server stdout")
			fmt.Fprint(stderrWriter, "server stderr")

			for _, want := range []string{"server stdout", "server stderr"} {
				if !strings.Contains(logOutput.String(), want) {
					t.Fatalf("log output missing %q:\n%s", want, logOutput.String())
				}
			}
			if mode == testConsoleOutputFailures {
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Fatalf("expected console output to be suppressed, got stdout %q and stderr %q", stdout.String(), stderr.String())
				}
			} else {
				if !strings.Contains(stdout.String(), "server stdout") {
					t.Fatalf("stdout was not streamed:\n%s", stdout.String())
				}
				if !strings.Contains(stderr.String(), "server stderr") {
					t.Fatalf("stderr was not streamed:\n%s", stderr.String())
				}
			}
		})
	}
}

func TestGoTestJSONWriterAttributesFailedSubtestOutput(t *testing.T) {
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	events := []goTestEvent{
		{Time: start, Action: "run", Package: "example.com/pkg", Test: "TestSuite"},
		{Time: start.Add(time.Second), Action: "run", Package: "example.com/pkg", Test: "TestSuite/TestFailed"},
		{
			Time:    start.Add(2 * time.Second),
			Action:  "output",
			Package: "example.com/pkg",
			Test:    "TestSuite/TestFailed",
			Output:  "    suite_test.go:42: boom\n",
		},
		{
			Time:    start.Add(2 * time.Second),
			Action:  "output",
			Package: "example.com/pkg",
			Test:    "TestSuite",
			Output:  "    Error: intentional parent-attributed failure\n",
		},
		{
			Time:    start.Add(3 * time.Second),
			Action:  "fail",
			Package: "example.com/pkg",
			Test:    "TestSuite/TestFailed",
		},
		{Time: start.Add(4 * time.Second), Action: "fail", Package: "example.com/pkg", Test: "TestSuite"},
	}
	var encoded bytes.Buffer
	for _, event := range events {
		if err := json.NewEncoder(&encoded).Encode(event); err != nil {
			t.Fatal(err)
		}
	}

	var raw, decoded bytes.Buffer
	results := newGoTestResults()
	writer := &goTestJSONWriter{
		rawWriter:    &raw,
		outputWriter: &decoded,
		results:      results,
	}
	data := encoded.Bytes()
	split := len(data) / 2
	if _, err := writer.Write(data[:split]); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data[split:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}

	rows := results.failures()
	if len(rows) != 2 {
		t.Fatalf("expected parent and child failures, got %#v", rows)
	}
	if !strings.Contains(rows[0].Details, "intentional parent-attributed failure") {
		t.Fatalf("parent-attributed failure output was not retained: %q", rows[0].Details)
	}
	row := rows[1]
	if row.Test != "TestSuite/TestFailed" || row.Package != "example.com/pkg" {
		t.Fatalf("unexpected failed test: %#v", row)
	}
	if !strings.Contains(row.Details, "suite_test.go:42: boom") {
		t.Fatalf("failed test output was not attributed correctly: %q", row.Details)
	}
	if !row.StartTime.Equal(start.Add(time.Second)) || !row.EndTime.Equal(start.Add(3*time.Second)) {
		t.Fatalf("unexpected test time window: %v to %v", row.StartTime, row.EndTime)
	}
	if raw.String() != encoded.String() {
		t.Fatalf("raw JSON output changed:\n%s", raw.String())
	}
	if decoded.String() != "    suite_test.go:42: boom\n    Error: intentional parent-attributed failure\n" {
		t.Fatalf("unexpected decoded output: %q", decoded.String())
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
	output.rerunCommand = "go run . unit-test"
	combinedPath, err := b.prepareLogPath(defaultTestLogDir, "combined.log")
	if err != nil {
		t.Fatal(err)
	}
	combinedFile, err := os.OpenFile(combinedPath, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		t.Fatal(err)
	}
	output.combinedLogPath = combinedPath
	output.combinedWriter = &lockedWriter{writer: combinedFile}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunTestCmdHelperProcess$")
	cmd.Env = append(os.Environ(), "TEMPORAL_RUN_TEST_CMD_HELPER=fail")
	if err := b.runTestCmd(cmd, output); err == nil {
		t.Fatal("expected command to fail")
	}
	if err := combinedFile.Close(); err != nil {
		t.Fatal(err)
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
	for _, want := range []string{
		"Failed tests (1)",
		"example.com/pkg / TestFailed",
		`go run . unit-test -run "^TestFailed$"`,
		"Go test JSON:",
		"Combined Go and dev server:",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("failure report missing %q:\n%s", want, stderr.String())
		}
	}
	jsonData, err := os.ReadFile(output.jsonLogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonData), `"Action":"fail"`) {
		t.Fatalf("JSON log missing failure event:\n%s", jsonData)
	}
	combinedData, err := os.ReadFile(combinedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(combinedData), "main_test.go:10: boom") {
		t.Fatalf("combined log missing decoded test output:\n%s", combinedData)
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

func TestWriteStructuredTestFailureReportFallsBackToCommandOutput(t *testing.T) {
	var output bytes.Buffer
	err := writeStructuredTestFailureReport(
		&output,
		nil,
		"# example.com/pkg\n./main.go:10: undefined: missing\nFAIL\n",
		testOutput{
			logPath:     ".build/test-logs/unit-test.log",
			jsonLogPath: ".build/test-logs/unit-test.json",
		},
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

func TestWriteStructuredTestFailureReportListsAllFailuresWhenDetailsAreOmitted(t *testing.T) {
	var rows []testFailureSummaryRow
	for i := 1; i <= 6; i++ {
		rows = append(rows, testFailureSummaryRow{
			Package: "example.com/pkg",
			Test:    fmt.Sprintf("TestFailed%d", i),
			Details: strings.Repeat("x", 20*1024),
		})
	}

	var snippets bytes.Buffer
	err := writeStructuredTestFailureReport(
		&snippets,
		rows,
		"",
		testOutput{
			logPath:     ".build/test-logs/unit-test.log",
			jsonLogPath: ".build/test-logs/unit-test.json",
		},
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
		"Failed Go test output (up to 16 KiB per test and 64 KiB total)",
		"... (truncated; see full test log) ...",
		"omitted Go output for 2 additional failed tests after reaching the 64 KiB total console limit",
	} {
		if !strings.Contains(snippets.String(), want) {
			t.Fatalf("failure output missing %q:\n%s", want, snippets.String())
		}
	}
}

func TestCollectServerFailureContexts(t *testing.T) {
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	logPath := filepath.Join(t.TempDir(), "dev-server.log")
	serverOutput := strings.Join([]string{
		`time=2026-07-29T11:59:00Z level=ERROR msg="mentions test" queue=TestSuite/TestFailed`,
		`time=2026-07-29T12:00:05Z level=WARN msg="inside test window"`,
		`time=2026-07-29T12:00:06Z level=INFO msg="not a warning or error"`,
		`time=2026-07-29T12:02:00Z level=ERROR msg="outside test window"`,
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(serverOutput), 0666); err != nil {
		t.Fatal(err)
	}
	row := testFailureSummaryRow{
		Package:   "example.com/pkg",
		Test:      "TestSuite/TestFailed",
		StartTime: start,
		EndTime:   start.Add(10 * time.Second),
	}

	contexts, err := collectServerFailureContexts(logPath, []testFailureSummaryRow{row})
	if err != nil {
		t.Fatal(err)
	}
	context := contexts[testFailureSummaryKey{Package: row.Package, Test: row.Test}]
	if context == nil {
		t.Fatal("expected server context")
	}
	if !strings.Contains(context.related.text.String(), "mentions test") {
		t.Fatalf("expected directly related server line:\n%s", context.related.text.String())
	}
	if !strings.Contains(context.window.text.String(), "inside test window") {
		t.Fatalf("expected time-window server line:\n%s", context.window.text.String())
	}
	for _, unexpected := range []string{"not a warning or error", "outside test window"} {
		if strings.Contains(context.related.text.String()+context.window.text.String(), unexpected) {
			t.Fatalf("unexpected server line %q was included", unexpected)
		}
	}
}

func TestWriteStructuredTestFailureReportIncludesArtifactAndServerContext(t *testing.T) {
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	logDir := t.TempDir()
	serverLogPath := filepath.Join(logDir, "dev-server.log")
	if err := os.WriteFile(
		serverLogPath,
		[]byte(`time=2026-07-29T12:00:01Z level=ERROR msg="server boom" queue=TestSuite/TestFailed`+"\n"),
		0666,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_LOG_ARTIFACT_NAME", "integ-test-ubuntu-latest-stable")
	t.Setenv("GITHUB_RUN_ID", "12345")
	var report bytes.Buffer
	err := writeStructuredTestFailureReport(
		&report,
		[]testFailureSummaryRow{
			{
				Package:   "example.com/pkg",
				Test:      "TestSuite",
				Details:   "    Error: parent-attributed failure\n",
				StartTime: start,
				EndTime:   start.Add(3 * time.Second),
			},
			{
				Package:   "example.com/pkg",
				Test:      "TestSuite/TestFailed",
				Details:   "    suite_test.go:42: boom\n",
				StartTime: start,
				EndTime:   start.Add(2 * time.Second),
			},
		},
		"",
		testOutput{
			logPath:         filepath.Join(logDir, "go-test.log"),
			jsonLogPath:     filepath.Join(logDir, "go-test.json"),
			combinedLogPath: filepath.Join(logDir, "combined.log"),
			serverLogPath:   serverLogPath,
			rerunCommand:    "go run . integration-test -dev-server",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"TEST FAILURE REPORT",
		"example.com/pkg / TestSuite",
		"example.com/pkg / TestSuite/TestFailed",
		"parent-attributed failure",
		`go run . integration-test -dev-server -run "^TestSuite/TestFailed$"`,
		"suite_test.go:42: boom",
		"Lines mentioning the failed test",
		`msg="server boom"`,
		"Combined Go and dev server:",
		"CI artifact: integ-test-ubuntu-latest-stable",
		"gh run download 12345 -n integ-test-ubuntu-latest-stable -D .build/ci-debug",
	} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("failure report missing %q:\n%s", want, report.String())
		}
	}
}

func TestRunTestCmdHelperProcess(t *testing.T) {
	switch os.Getenv("TEMPORAL_RUN_TEST_CMD_HELPER") {
	case "":
		return
	case "pass":
		writeGoTestEvent(goTestEvent{
			Time:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			Action:  "output",
			Package: "example.com/pkg",
			Test:    "TestPassed",
			Output:  "stdout output\n",
		})
		writeGoTestEvent(goTestEvent{
			Time:    time.Date(2026, 7, 29, 12, 0, 1, 0, time.UTC),
			Action:  "pass",
			Package: "example.com/pkg",
			Test:    "TestPassed",
		})
		fmt.Fprintln(os.Stderr, "stderr output")
	case "fail":
		start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		writeGoTestEvent(goTestEvent{
			Time:    start,
			Action:  "run",
			Package: "example.com/pkg",
			Test:    "TestFailed",
		})
		for _, output := range []string{
			"=== RUN   TestFailed\n",
			"    main_test.go:10: boom\n",
			"--- FAIL: TestFailed (0.00s)\n",
		} {
			writeGoTestEvent(goTestEvent{
				Time:    start.Add(time.Second),
				Action:  "output",
				Package: "example.com/pkg",
				Test:    "TestFailed",
				Output:  output,
			})
		}
		writeGoTestEvent(goTestEvent{
			Time:    start.Add(2 * time.Second),
			Action:  "fail",
			Package: "example.com/pkg",
			Test:    "TestFailed",
		})
		writeGoTestEvent(goTestEvent{
			Time:    start.Add(2 * time.Second),
			Action:  "output",
			Package: "example.com/pkg",
			Output:  "FAIL\nFAIL\texample.com/pkg\t0.001s\n",
		})
		os.Exit(1)
	default:
		t.Fatalf("unexpected helper mode")
	}
}

func writeGoTestEvent(event goTestEvent) {
	if err := json.NewEncoder(os.Stdout).Encode(event); err != nil {
		panic(err)
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
