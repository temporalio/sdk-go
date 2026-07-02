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

func TestParseGoTestListOutput(t *testing.T) {
	tests := parseGoTestListOutput(strings.Join([]string{
		"TestB",
		"ok  \tgo.temporal.io/sdk/test\t0.001s",
		"?   \tgo.temporal.io/sdk/test/nexusclient\t[no test files]",
		"TestA",
		"TestB",
	}, "\n"))

	if got, want := strings.Join(tests, ","), "TestA,TestB"; got != want {
		t.Fatalf("unexpected tests: got %q want %q", got, want)
	}
}

func TestValidateIntegrationShardManifest(t *testing.T) {
	manifest := &integrationShardManifest{
		IntegrationShards: map[string][]string{
			"a": {"TestA"},
			"b": {"TestB"},
		},
	}

	if err := validateIntegrationShardManifest(manifest, []string{"TestA", "TestB"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateIntegrationShardManifestAllowsRegexShard(t *testing.T) {
	manifest := &integrationShardManifest{
		IntegrationShards: map[string][]string{
			"a": {integrationShardRegexPrefix + "TestA/(One|Two)"},
			"b": {"TestB"},
		},
	}

	if err := validateIntegrationShardManifest(manifest, []string{"TestA", "TestB"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateIntegrationShardManifestRejectsMixedRegexShard(t *testing.T) {
	manifest := &integrationShardManifest{
		IntegrationShards: map[string][]string{
			"a": {integrationShardRegexPrefix + "TestA/One", "TestB"},
		},
	}

	err := validateIntegrationShardManifest(manifest, []string{"TestA", "TestB"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), `integration shard "a" cannot mix an explicit regex with other entries`) {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

func TestValidateIntegrationShardManifestDetectsProblems(t *testing.T) {
	manifest := &integrationShardManifest{
		IntegrationShards: map[string][]string{
			"a": {"TestA", "TestUnknown"},
			"b": {"TestA"},
		},
	}

	err := validateIntegrationShardManifest(manifest, []string{"TestA", "TestMissing"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, want := range []string{
		"unassigned integration tests: TestMissing",
		"unknown integration tests in shard manifest: TestUnknown",
		"integration tests assigned to multiple shards: TestA in a, b",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %q", want, err.Error())
		}
	}
}

func TestValidateIntegrationShardManifestDetectsDirectAndRegexDuplicate(t *testing.T) {
	manifest := &integrationShardManifest{
		IntegrationShards: map[string][]string{
			"a": {integrationShardRegexPrefix + "TestA/One"},
			"b": {"TestA"},
		},
	}

	err := validateIntegrationShardManifest(manifest, []string{"TestA"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "TestA assigned both directly and by regex") {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

func TestIntegrationShardRegexForNameAnchorsDirectTests(t *testing.T) {
	builder := newTestIntegrationShardBuilder(t, strings.Join([]string{
		"integration-shards:",
		"  a:",
		"    - TestA",
		"    - TestB",
		"    - TestIntegrationSuite",
		"",
	}, "\n"))

	got, err := builder.integrationShardRegexForName("a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "^(TestA|TestB|TestIntegrationSuite)$" {
		t.Fatalf("unexpected shard regex: %q", got)
	}
}

func TestIntegrationShardRegexForNamePreservesExplicitRegex(t *testing.T) {
	builder := newTestIntegrationShardBuilder(t, strings.Join([]string{
		"integration-shards:",
		"  a:",
		"    - \"@regex:TestIntegrationSuite/Test(A|B).*\"",
		"  b:",
		"    - TestA",
		"    - TestB",
		"",
	}, "\n"))

	got, err := builder.integrationShardRegexForName("a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "TestIntegrationSuite/Test(A|B).*" {
		t.Fatalf("unexpected shard regex: %q", got)
	}
}

func newTestIntegrationShardBuilder(t *testing.T, manifest string) *builder {
	t.Helper()

	rootDir := t.TempDir()
	for _, dir := range []string{
		filepath.Join(rootDir, ".github"),
		filepath.Join(rootDir, "test"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(rootDir, "go.mod"):                            "module example.com/integration-shards\n\ngo 1.24\n",
		filepath.Join(rootDir, ".github", "integration-shards.yml"): manifest,
		filepath.Join(rootDir, "test", "integration_test.go"): strings.Join([]string{
			"package test",
			"",
			"import \"testing\"",
			"",
			"func TestA(t *testing.T) {}",
			"func TestB(t *testing.T) {}",
			"func TestIntegrationSuite(t *testing.T) {",
			"\tt.Run(\"TestA\", func(t *testing.T) {})",
			"\tt.Run(\"TestB\", func(t *testing.T) {})",
			"}",
			"",
		}, "\n"),
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &builder{rootDir: rootDir}
}
