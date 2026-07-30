package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const testFailureSnippetMaxDetailBytes = 16 * 1024
const testFailureSnippetMaxTotalBytes = 64 * 1024
const testServerSnippetMaxDetailBytes = 16 * 1024
const testServerSnippetMaxTotalBytes = 64 * 1024
const testServerContextPadding = 2 * time.Second

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}

type goTestEvent struct {
	Time    time.Time
	Action  string
	Package string
	Test    string
	Output  string
}

type goTestJSONWriter struct {
	rawWriter    io.Writer
	outputWriter io.Writer
	results      *goTestResults
	pending      []byte
}

func (w *goTestJSONWriter) Write(p []byte) (int, error) {
	n, err := w.rawWriter.Write(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	w.pending = append(w.pending, p...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		line := append([]byte(nil), w.pending[:newline+1]...)
		w.pending = w.pending[newline+1:]
		if err := w.processLine(line); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

func (w *goTestJSONWriter) Flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	line := append([]byte(nil), w.pending...)
	w.pending = nil
	return w.processLine(line)
}

func (w *goTestJSONWriter) processLine(line []byte) error {
	var event goTestEvent
	if err := json.Unmarshal(bytes.TrimSpace(line), &event); err != nil {
		if _, writeErr := w.outputWriter.Write(line); writeErr != nil {
			return writeErr
		}
		_, _ = w.results.recordRawOutput(line)
		return nil
	}
	w.results.recordEvent(event)
	if event.Output != "" {
		_, err := io.WriteString(w.outputWriter, event.Output)
		return err
	}
	return nil
}

type goTestResults struct {
	mu       sync.Mutex
	tests    map[testFailureSummaryKey]*goTestRecord
	fallback strings.Builder
}

type goTestRecord struct {
	start   time.Time
	end     time.Time
	failed  bool
	details strings.Builder
}

func newGoTestResults() *goTestResults {
	return &goTestResults{tests: make(map[testFailureSummaryKey]*goTestRecord)}
}

func (r *goTestResults) recordEvent(event goTestEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Test == "" {
		if event.Output != "" {
			r.fallback.WriteString(event.Output)
		}
		return
	}
	key := testFailureSummaryKey{Package: event.Package, Test: event.Test}
	record := r.tests[key]
	if record == nil {
		record = &goTestRecord{}
		r.tests[key] = record
	}
	if record.start.IsZero() && !event.Time.IsZero() {
		record.start = event.Time
	}
	switch event.Action {
	case "output":
		record.details.WriteString(event.Output)
	case "fail":
		record.failed = true
		record.end = event.Time
	case "pass", "skip":
		delete(r.tests, key)
	}
}

func (r *goTestResults) recordRawOutput(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fallback.Write(p)
}

func (r *goTestResults) failures() []testFailureSummaryRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]testFailureSummaryRow, 0)
	for key, record := range r.tests {
		if !record.failed {
			continue
		}
		details := record.details.String()
		if strings.TrimSpace(details) == "" {
			details = fmt.Sprintf("--- FAIL: %s", key.Test)
		}
		rows = append(rows, testFailureSummaryRow{
			Test:      key.Test,
			Package:   key.Package,
			Details:   details,
			StartTime: record.start,
			EndTime:   record.end,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Package != rows[j].Package {
			return rows[i].Package < rows[j].Package
		}
		return rows[i].Test < rows[j].Test
	})
	return rows
}

func (r *goTestResults) fallbackOutput() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var output strings.Builder
	output.WriteString(r.fallback.String())
	keys := make([]testFailureSummaryKey, 0, len(r.tests))
	for key := range r.tests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Package != keys[j].Package {
			return keys[i].Package < keys[j].Package
		}
		return keys[i].Test < keys[j].Test
	})
	for _, key := range keys {
		output.WriteString(r.tests[key].details.String())
	}
	return output.String()
}

type testFailureSummaryRow struct {
	Test      string
	Package   string
	Details   string
	StartTime time.Time
	EndTime   time.Time
}

func writeStructuredTestFailureReport(
	w io.Writer,
	rows []testFailureSummaryRow,
	fallback string,
	output testOutput,
) error {
	var sb strings.Builder
	sb.WriteString("\nTEST FAILURE REPORT\n")
	if len(rows) == 0 {
		fmt.Fprintf(
			&sb,
			"\nNo individual failed test was identified; showing up to %d KiB of command output:\n\n",
			testFailureSnippetMaxTotalBytes/1024,
		)
		sb.WriteString(truncateTestFailureSnippet(fallback, testFailureSnippetMaxTotalBytes))
		sb.WriteString("\n")
		writeCompleteTestLogLocations(&sb, output)
		return writeString(w, sb.String())
	}

	fmt.Fprintf(&sb, "\nFailed tests (%d):\n", len(rows))
	for _, row := range rows {
		fmt.Fprintf(&sb, "- %s\n", testFailureTitle(row))
	}
	if output.rerunCommand != "" {
		sb.WriteString("\nRerun from internal/cmd/build:\n")
		for _, row := range rows {
			testPattern := "^" + regexp.QuoteMeta(row.Test) + "$"
			fmt.Fprintf(&sb, "- %s -run %s\n", output.rerunCommand, strconv.Quote(testPattern))
		}
	}

	fmt.Fprintf(
		&sb,
		"\nFailed Go test output (up to %d KiB per test and %d KiB total):\n",
		testFailureSnippetMaxDetailBytes/1024,
		testFailureSnippetMaxTotalBytes/1024,
	)
	remainingGoOutput := testFailureSnippetMaxTotalBytes
	for i, row := range rows {
		if remainingGoOutput <= 0 {
			fmt.Fprintf(
				&sb,
				"\n... omitted Go output for %d additional failed tests after reaching the %d KiB total console limit; all failed tests are listed above\n",
				len(rows)-i,
				testFailureSnippetMaxTotalBytes/1024,
			)
			break
		}
		fmt.Fprintf(&sb, "\n--- %s ---\n", testFailureTitle(row))
		maxDetailBytes := min(testFailureSnippetMaxDetailBytes, remainingGoOutput)
		details := truncateTestFailureSnippet(row.Details, maxDetailBytes)
		sb.WriteString(details)
		sb.WriteString("\n")
		remainingGoOutput -= len(details)
	}

	if output.serverLogPath != "" {
		serverRows := filterParentFailureRows(rows)
		contexts, err := collectServerFailureContexts(output.serverLogPath, serverRows)
		fmt.Fprintf(
			&sb,
			"\nRelated dev server WARN/ERROR output (best effort, up to %d KiB per test and %d KiB total):\n",
			testServerSnippetMaxDetailBytes/1024,
			testServerSnippetMaxTotalBytes/1024,
		)
		if err != nil {
			fmt.Fprintf(&sb, "Unable to read dev server context: %v\n", err)
		} else {
			writeServerFailureContexts(&sb, serverRows, contexts, output.serverLogPath)
		}
	}

	writeCompleteTestLogLocations(&sb, output)
	return writeString(w, sb.String())
}

func writeCompleteTestLogLocations(sb *strings.Builder, output testOutput) {
	sb.WriteString("\nComplete logs:\n")
	fmt.Fprintf(sb, "- Go test: %s\n", output.logPath)
	fmt.Fprintf(sb, "- Go test JSON: %s\n", output.jsonLogPath)
	if output.combinedLogPath != "" {
		fmt.Fprintf(sb, "- Combined Go and dev server: %s\n", output.combinedLogPath)
	}
	if output.serverLogPath != "" {
		fmt.Fprintf(sb, "- Dev server: %s\n", output.serverLogPath)
	}
	if artifactName := strings.TrimSpace(os.Getenv("TEST_LOG_ARTIFACT_NAME")); artifactName != "" {
		fmt.Fprintf(sb, "- CI artifact: %s\n", artifactName)
		if runID := strings.TrimSpace(os.Getenv("GITHUB_RUN_ID")); runID != "" {
			command := formatShellCommand([]string{
				"gh", "run", "download", runID, "-n", artifactName, "-D", ".build/ci-debug",
			})
			fmt.Fprintf(sb, "- Download: %s\n", command)
		}
	}
}

func writeString(w io.Writer, value string) error {
	_, err := io.WriteString(w, value)
	return err
}

type serverFailureContext struct {
	related limitedLogCapture
	window  limitedLogCapture
}

type limitedLogCapture struct {
	text         strings.Builder
	maxBytes     int
	omittedLines int
}

func (c *limitedLogCapture) add(line string) {
	lineBytes := len(line) + 1
	if c.text.Len()+lineBytes > c.maxBytes {
		c.omittedLines++
		return
	}
	c.text.WriteString(line)
	c.text.WriteByte('\n')
}

func (c *limitedLogCapture) appendTo(sb *strings.Builder, heading string) {
	if c.text.Len() == 0 && c.omittedLines == 0 {
		return
	}
	fmt.Fprintf(sb, "%s:\n", heading)
	sb.WriteString(c.text.String())
	if c.omittedLines > 0 {
		fmt.Fprintf(sb, "... omitted %d additional matching server log lines\n", c.omittedLines)
	}
}

func collectServerFailureContexts(
	logPath string,
	rows []testFailureSummaryRow,
) (map[testFailureSummaryKey]*serverFailureContext, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	contexts := make(map[testFailureSummaryKey]*serverFailureContext, len(rows))
	for _, row := range rows {
		contexts[testFailureSummaryKey{Package: row.Package, Test: row.Test}] = &serverFailureContext{
			related: limitedLogCapture{maxBytes: testServerSnippetMaxDetailBytes},
			window:  limitedLogCapture{maxBytes: testServerSnippetMaxDetailBytes},
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !isServerWarningOrError(line) {
			continue
		}
		lineTime, hasLineTime := serverLogTime(line)
		for _, row := range rows {
			key := testFailureSummaryKey{Package: row.Package, Test: row.Test}
			context := contexts[key]
			if serverLineMatchesTest(line, row.Test) {
				context.related.add(line)
				continue
			}
			if hasLineTime && testTimeWindowContains(row, lineTime) {
				context.window.add(line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return contexts, nil
}

func isServerWarningOrError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "level=warn") ||
		strings.Contains(lower, "level=error") ||
		strings.Contains(lower, `"level":"warn"`) ||
		strings.Contains(lower, `"level":"error"`)
}

func serverLineMatchesTest(line, testName string) bool {
	if strings.Contains(line, testName) {
		return true
	}
	if slash := strings.LastIndexByte(testName, '/'); slash >= 0 {
		return strings.Contains(line, testName[slash+1:])
	}
	return false
}

func serverLogTime(line string) (time.Time, bool) {
	index := strings.Index(line, "time=")
	if index < 0 {
		return time.Time{}, false
	}
	value := line[index+len("time="):]
	if space := strings.IndexByte(value, ' '); space >= 0 {
		value = value[:space]
	}
	value = strings.Trim(value, `"`)
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func testTimeWindowContains(row testFailureSummaryRow, value time.Time) bool {
	if row.StartTime.IsZero() || row.EndTime.IsZero() {
		return false
	}
	start := row.StartTime.Add(-testServerContextPadding)
	end := row.EndTime.Add(testServerContextPadding)
	return !value.Before(start) && !value.After(end)
}

func writeServerFailureContexts(
	sb *strings.Builder,
	rows []testFailureSummaryRow,
	contexts map[testFailureSummaryKey]*serverFailureContext,
	logPath string,
) {
	remaining := testServerSnippetMaxTotalBytes
	for i, row := range rows {
		key := testFailureSummaryKey{Package: row.Package, Test: row.Test}
		context := contexts[key]
		if context == nil {
			continue
		}
		var details strings.Builder
		context.related.appendTo(&details, "Lines mentioning the failed test")
		context.window.appendTo(&details, "Other WARN/ERROR lines during the test window")
		if details.Len() == 0 {
			continue
		}
		if remaining <= 0 {
			fmt.Fprintf(
				sb,
				"... omitted server context for %d additional failed tests after reaching the %d KiB total console limit\n",
				len(rows)-i,
				testServerSnippetMaxTotalBytes/1024,
			)
			return
		}
		fmt.Fprintf(sb, "\n--- %s ---\n", testFailureTitle(row))
		maxDetailBytes := min(testServerSnippetMaxDetailBytes, remaining)
		rendered := truncateServerFailureSnippet(details.String(), maxDetailBytes, logPath)
		sb.WriteString(rendered)
		if !strings.HasSuffix(rendered, "\n") {
			sb.WriteByte('\n')
		}
		remaining -= len(rendered)
	}
	if remaining == testServerSnippetMaxTotalBytes {
		sb.WriteString("No dev server WARN/ERROR lines could be correlated with the failed tests.\n")
	}
}

func truncateServerFailureSnippet(value string, maxBytes int, logPath string) string {
	marker := fmt.Sprintf("\n... (truncated; see complete dev server log at %s) ...\n", logPath)
	return truncateWithMarker(value, maxBytes, marker)
}

func truncateWithMarker(value string, maxBytes int, marker string) string {
	if len(value) <= maxBytes {
		return value
	}
	if maxBytes <= len(marker) {
		return marker[:maxBytes]
	}
	prefixBytes := (maxBytes - len(marker)) / 2
	suffixBytes := maxBytes - len(marker) - prefixBytes
	return value[:prefixBytes] + marker + value[len(value)-suffixBytes:]
}

func formatShellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.IndexFunc(arg, shellCommandArgNeedsQuoting) < 0 {
			quoted[i] = arg
		} else {
			quoted[i] = strconv.Quote(arg)
		}
	}
	return strings.Join(quoted, " ")
}

func shellCommandArgNeedsQuoting(r rune) bool {
	return !(r >= 'a' && r <= 'z') &&
		!(r >= 'A' && r <= 'Z') &&
		!(r >= '0' && r <= '9') &&
		!strings.ContainsRune("-._/", r)
}

func truncateTestFailureSnippet(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	const marker = "\n... (truncated; see full test log) ...\n"
	if maxBytes <= len(marker) {
		return value[:maxBytes]
	}
	prefixBytes := (maxBytes - len(marker)) / 2
	suffixBytes := maxBytes - len(marker) - prefixBytes
	return value[:prefixBytes] + marker + value[len(value)-suffixBytes:]
}

func testFailureTitle(row testFailureSummaryRow) string {
	if row.Package == "" {
		return row.Test
	}
	return row.Package + " / " + row.Test
}

func filterParentFailureRows(rows []testFailureSummaryRow) []testFailureSummaryRow {
	hasFailedSubtest := make(map[testFailureSummaryKey]bool, len(rows))
	for _, row := range rows {
		if parent, _, ok := strings.Cut(row.Test, "/"); ok {
			hasFailedSubtest[testFailureSummaryKey{Package: row.Package, Test: parent}] = true
		}
	}
	filtered := rows[:0]
	for _, row := range rows {
		if !hasFailedSubtest[testFailureSummaryKey{Package: row.Package, Test: row.Test}] {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

type testFailureSummaryKey struct {
	Package string
	Test    string
}
