package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	_ "github.com/BurntSushi/toml"
	_ "github.com/kisielk/errcheck/errcheck"
	_ "honnef.co/go/tools/staticcheck"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/cliversion"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func main() {
	if err := newBuilder().run(); err != nil {
		log.Fatal(err)
	}
}

const coverageDir = ".build/coverage"
const githubStepSummaryMaxDetailBytes = 64 * 1024

type builder struct {
	thisDir string
	rootDir string
}

func newBuilder() *builder {
	var b builder
	// Find the root directory from this directory
	_, thisFile, _, _ := runtime.Caller(0)
	b.thisDir = filepath.Join(thisFile, "..")
	b.rootDir = filepath.Join(b.thisDir, "../../../")
	return &b
}

func (b *builder) run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("missing command name, 'check', 'integration-test', or 'unit-test' required")
	}
	switch os.Args[1] {
	case "check":
		return b.check()
	case "integration-test":
		return b.integrationTest()
	case "merge-coverage-files":
		return b.mergeCoverageFiles()
	case "unit-test":
		return b.unitTest()
	default:
		return fmt.Errorf("unrecognized command %q, 'check', 'integration-test', or 'unit-test' required", os.Args[1])
	}
}

func (b *builder) check() error {
	// Run go vet
	if err := b.runCmd(b.cmdFromRoot("go", "vet", "./...")); err != nil {
		return fmt.Errorf("go vet failed: %w", err)
	}
	// Run errcheck
	if errCheck, err := b.getInstalledTool("github.com/kisielk/errcheck"); err != nil {
		return fmt.Errorf("failed getting errcheck: %w", err)
	} else if err := b.runCmd(b.cmdFromRoot(errCheck, "./...")); err != nil {
		return fmt.Errorf("errcheck failed: %w", err)
	}
	// Run staticcheck
	if staticCheck, err := b.getInstalledTool("honnef.co/go/tools/cmd/staticcheck"); err != nil {
		return fmt.Errorf("failed getting staticcheck: %w", err)
	} else if err := b.runCmd(b.cmdFromRoot(staticCheck, "./...")); err != nil {
		return fmt.Errorf("staticcheck failed: %w", err)
	}
	// Run doclink check
	if err := b.runCmd(b.cmdFromRoot("go", "run", "./internal/cmd/tools/doclink/doclink.go")); err != nil {
		return fmt.Errorf("doclink check failed: %w", err)
	}
	return nil
}

func (b *builder) integrationTest() error {
	// Supports some flags
	flagSet := flag.NewFlagSet("integration-test", flag.ContinueOnError)
	runFlag := flagSet.String("run", "", "Passed to go test as -run")
	pFlag := flagSet.String("p", "", "Passed to go test as -p")
	packagesFlag := flagSet.String("packages", "./...", "Packages passed to go test")
	devServerFlag := flagSet.Bool("dev-server", false, "Use an embedded dev server")
	coverageFileFlag := flagSet.String("coverage-file", "", "If set, enables coverage output to this filename")
	if err := flagSet.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed parsing flags: %w", err)
	}

	// Also accept coverage file as env var
	if env := strings.TrimSpace(os.Getenv("TEMPORAL_COVERAGE_FILE")); *coverageFileFlag == "" && env != "" {
		*coverageFileFlag = env
	}

	// Create coverage dir if doing coverage
	if *coverageFileFlag != "" {
		if err := os.MkdirAll(filepath.Join(b.rootDir, coverageDir), 0777); err != nil {
			return fmt.Errorf("failed creating coverage dir: %w", err)
		}
	}

	customKeyField := temporal.NewSearchAttributeKeyKeyword("CustomKeywordField")
	customStringField := temporal.NewSearchAttributeKeyString("CustomStringField")
	searchAttributes := temporal.NewSearchAttributes(
		customKeyField.ValueSet("Keyword"),
		customStringField.ValueSet("Text"),
	)

	// Start dev server if wanted
	if *devServerFlag {
		devServer, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{
			CachedDownload: testsuite.CachedDownload{
				Version: cliversion.CLIVersion,
			},
			ClientOptions: &client.Options{
				HostPort:  "127.0.0.1:7233",
				Namespace: "integration-test-namespace",
			},
			DBFilename:       "temporal.sqlite",
			LogLevel:         "warn",
			SearchAttributes: searchAttributes,
			ExtraArgs: []string{
				"--sqlite-pragma", "journal_mode=WAL",
				"--sqlite-pragma", "synchronous=OFF",
				"--dynamic-config-value", "frontend.enableExecuteMultiOperation=true",
				"--dynamic-config-value", "frontend.enableUpdateWorkflowExecution=true",
				"--dynamic-config-value", "frontend.enableUpdateWorkflowExecutionAsyncAccepted=true",
				"--dynamic-config-value", "frontend.workerVersioningRuleAPIs=true",
				"--dynamic-config-value", "frontend.workerVersioningDataAPIs=true",
				"--dynamic-config-value", "frontend.workerVersioningWorkflowAPIs=true",
				"--dynamic-config-value", "system.enableActivityEagerExecution=true",
				"--dynamic-config-value", "system.enableEagerWorkflowStart=true",
				"--dynamic-config-value", "system.forceSearchAttributesCacheRefreshOnRead=true",
				"--dynamic-config-value", "worker.buildIdScavengerEnabled=true",
				"--dynamic-config-value", "worker.removableBuildIdDurationSinceDefault=1",
				"--dynamic-config-value", "system.enableDeployments=true",
				"--dynamic-config-value", "system.enableDeploymentVersions=true",
				"--dynamic-config-value", "matching.wv.VersionDrainageStatusVisibilityGracePeriod=10",
				"--dynamic-config-value", "matching.wv.VersionDrainageStatusRefreshInterval=1",
				"--dynamic-config-value", "matching.useNewMatcher=true",
				"--dynamic-config-value", "frontend.activityAPIsEnabled=true",
				"--dynamic-config-value", "frontend.enableCancelWorkerPollsOnShutdown=true",
				"--http-port", "7243", // Nexus tests use the HTTP port directly
				"--dynamic-config-value", `component.callbacks.allowedAddresses=[{"Pattern":"*","AllowInsecure":true}]`, // SDK tests use arbitrary callback URLs, permit that on the server
				"--dynamic-config-value", `system.refreshNexusEndpointsMinWait="0s"`, // Make Nexus tests faster
				"--dynamic-config-value", `component.nexusoperations.recordCancelRequestCompletionEvents=true`, // Defaults to false until after OSS 1.28 is released
				"--dynamic-config-value", `history.enableRequestIdRefLinks=true`,
				"--dynamic-config-value", "activity.enableStandalone=true",
				"--dynamic-config-value", "history.enableChasm=true",
				"--dynamic-config-value", "history.enableTransitionHistory=true",
				"--dynamic-config-value", `component.nexusoperations.useSystemCallbackURL=false`,
				"--dynamic-config-value", `component.nexusoperations.callback.endpoint.template="http://localhost:7243/namespaces/{{.NamespaceName}}/nexus/callback"`,
				"--dynamic-config-value", "nexusoperation.enableStandalone=true",
				"--dynamic-config-value", "history.enableChasmCallbacks=true",
				"--dynamic-config-value", "frontend.ListWorkersEnabled=true",
			},
		})
		if err != nil {
			return fmt.Errorf("failed starting dev server: %w", err)
		}
		defer func() { _ = devServer.Stop() }()
	}

	// Run integration test
	args := []string{"go", "test", "-count", "1", "-race", "-v", "-timeout", "15m"}
	env := append(os.Environ(), "DISABLE_SERVER_1_25_TESTS=1")
	if *runFlag != "" {
		args = append(args, "-run", *runFlag)
	}
	if *pFlag != "" {
		args = append(args, "-p", *pFlag)
	}
	if *coverageFileFlag != "" {
		args = append(args, "-coverprofile="+filepath.Join(b.rootDir, coverageDir, *coverageFileFlag), "-coverpkg=./...")
	}
	args = append(args, strings.Fields(*packagesFlag)...)
	if *devServerFlag {
		args = append(args, "--", "-using-cli-dev-server")
		env = append(env, "TEMPORAL_NAMESPACE=integration-test-namespace")
	}
	// Must run in test dir
	cmd := b.cmdFromRoot(args...)
	cmd.Dir = filepath.Join(cmd.Dir, "test")
	cmd.Env = env
	if err := b.runTestCmd(cmd); err != nil {
		return fmt.Errorf("integration test failed: %w", err)
	}

	return nil
}

func (b *builder) mergeCoverageFiles() error {
	// Only arg should be out file
	if len(os.Args) != 3 {
		return fmt.Errorf("merge-coverage-files requires single out file")
	}
	// Basically we make a new file with a "mode:" line header, then write all
	// lines from all files except their "mode:" lines
	log.Printf("Merging coverage files to %v", os.Args[2])
	f, err := os.Create(os.Args[2])
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("mode: atomic\n"); err != nil {
		return err
	}
	coverageDirEntries, err := os.ReadDir(filepath.Join(b.rootDir, coverageDir))
	if err != nil {
		return fmt.Errorf("failed reading coverage dir: %w", err)
	}
	for _, entry := range coverageDirEntries {
		b, err := os.ReadFile(filepath.Join(b.rootDir, coverageDir, entry.Name()))
		if err != nil {
			return err
		}
		for _, line := range bytes.SplitAfter(b, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("mode:")) && len(bytes.TrimSpace(line)) > 0 {
				if _, err := f.Write(line); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (b *builder) unitTest() error {
	// Supports some flags
	flagSet := flag.NewFlagSet("unit-test", flag.ContinueOnError)
	runFlag := flagSet.String("run", "", "Passed to go test as -run")
	coverageFlag := flagSet.Bool("coverage", false, "If set, enables coverage output")
	if err := flagSet.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed parsing flags: %w", err)
	}

	// Find every non ./test-prefixed package that has a test file
	testDirMap := map[string]struct{}{}
	var testDirs []string
	err := fs.WalkDir(os.DirFS(b.rootDir), ".", func(p string, d fs.DirEntry, err error) error {
		if (!strings.HasPrefix(p, "test") || strings.HasPrefix(p, "testsuite")) && strings.HasSuffix(p, "_test.go") {
			dir := path.Dir(p)
			if _, ok := testDirMap[dir]; !ok {
				testDirMap[dir] = struct{}{}
				testDirs = append(testDirs, dir)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed walking test dirs: %w", err)
	}
	sort.Strings(testDirs)

	// Create coverage dir if doing coverage
	if *coverageFlag {
		if err := os.MkdirAll(filepath.Join(b.rootDir, coverageDir), 0777); err != nil {
			return fmt.Errorf("failed creating coverage dir: %w", err)
		}
	}

	// Run unit test for each dir
	log.Printf("Running unit tests in dirs: %v", testDirs)
	for _, testDir := range testDirs {
		// Run unit test
		args := []string{"go", "test", "-count", "1", "-race", "-v", "-timeout", "15m"}
		if *runFlag != "" {
			args = append(args, "-run", *runFlag)
		}
		if *coverageFlag {
			args = append(
				args,
				"-coverprofile="+filepath.Join(b.rootDir, coverageDir, "unit-test-"+strings.ReplaceAll(testDir, "/", "-")+".out"),
				"-coverpkg=./...",
			)
		}
		args = append(args, ".")
		cmd := b.cmdFromRoot(args...)
		// Need to run inside directory
		cmd.Dir = filepath.Join(b.rootDir, testDir)
		if err := b.runTestCmd(cmd); err != nil {
			return fmt.Errorf("unit test failed in %v: %w", testDir, err)
		}
	}

	return nil
}

func (b *builder) cmdFromRoot(args ...string) *exec.Cmd {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = b.rootDir
	return cmd
}

// Forwards stdout/stderr
func (b *builder) runCmd(cmd *exec.Cmd) error {
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	log.Printf("Running %v in %v with args %v", cmd.Path, cmd.Dir, cmd.Args[1:])
	return cmd.Run()
}

// runTestCmd runs a go test command while capturing output for the GitHub step
// summary. Output is still streamed to stdout/stderr as the command runs.
func (b *builder) runTestCmd(cmd *exec.Cmd) error {
	var output lockedBuffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &output)
	cmd.Stderr = io.MultiWriter(os.Stderr, &output)
	log.Printf("Running %v in %v with args %v", cmd.Path, cmd.Dir, cmd.Args[1:])
	err := cmd.Run()
	if err != nil {
		summaryErr := appendTestFailureSummary(os.Getenv("GITHUB_STEP_SUMMARY"), output.String())
		if summaryErr != nil {
			log.Printf("Failed writing test failure summary: %v", summaryErr)
		}
	}
	return err
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

type testFailureSummaryRow struct {
	Test    string
	Package string
	Details string
}

func appendTestFailureSummary(summaryPath, output string) error {
	if summaryPath == "" {
		return nil
	}
	rows := parseTestFailures(output)
	if len(rows) == 0 {
		return nil
	}
	f, err := os.OpenFile(summaryPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(renderTestFailureSummary(rows))
	return err
}

func parseTestFailures(output string) []testFailureSummaryRow {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	rows := make([]testFailureSummaryRow, 0)
	currentPackage := ""
	packageStart := 0
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "FAIL\t") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				currentPackage = fields[1]
				for rowIndex := packageStart; rowIndex < len(rows); rowIndex++ {
					if rows[rowIndex].Package == "" {
						rows[rowIndex].Package = currentPackage
					}
				}
				packageStart = len(rows)
			}
			continue
		}
		const failPrefix = "--- FAIL: "
		trimmedLine := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmedLine, failPrefix) {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(trimmedLine, failPrefix))
		if idx := strings.Index(name, " "); idx >= 0 {
			name = name[:idx]
		}
		start := findMatchingRunLine(lines, i, name)
		end := len(lines)
		for j := start + 1; j < len(lines); j++ {
			next := lines[j]
			if isRunLine(next) || strings.HasPrefix(next, "FAIL\t") || strings.HasPrefix(next, "ok  \t") {
				end = j
				break
			}
		}
		rows = append(rows, testFailureSummaryRow{
			Test:    name,
			Package: currentPackage,
			Details: strings.Join(lines[start:end], "\n"),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Package != rows[j].Package {
			return rows[i].Package < rows[j].Package
		}
		return rows[i].Test < rows[j].Test
	})
	return filterParentFailureRows(rows)
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

func findMatchingRunLine(lines []string, before int, testName string) int {
	for i := before - 1; i >= 0; i-- {
		if testNameFromRunLine(lines[i]) == testName {
			return i
		}
	}
	return before
}

func testNameFromRunLine(line string) string {
	fields := strings.Fields(strings.TrimLeft(line, " \t"))
	if len(fields) != 3 || fields[0] != "===" || fields[1] != "RUN" {
		return ""
	}
	return fields[2]
}

func isRunLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "=== RUN ")
}

func renderTestFailureSummary(rows []testFailureSummaryRow) string {
	var sb strings.Builder
	sb.WriteString("## Test failures\n\n")
	sb.WriteString("<table>\n<tr><th>Kind</th><th>Test failure</th></tr>\n")
	for _, row := range rows {
		details := row.Details
		if len(details) > githubStepSummaryMaxDetailBytes {
			details = details[:githubStepSummaryMaxDetailBytes] + "\n... (truncated; see full job logs)"
		}
		title := row.Test
		if row.Package != "" {
			title = row.Package + " / " + title
		}
		fmt.Fprintf(
			&sb,
			"<tr><td>%s</td><td><details><summary>%s</summary><pre>%s</pre></details></td></tr>\n",
			html.EscapeString("Failed"),
			html.EscapeString(title),
			html.EscapeString(details),
		)
	}
	sb.WriteString("</table>\n\n")
	return sb.String()
}

func (b *builder) getInstalledTool(modPath string) (string, error) {
	// Install
	log.Printf("Installing %v", modPath)
	cmd := exec.Command("go", "install", modPath)
	cmd.Dir = b.thisDir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed installing %q: %w", modPath, err)
	}

	// Get path to installed
	cmd = exec.Command("go", "list", "-f", "{{.Target}}", modPath)
	cmd.Dir = b.thisDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed listing path for tool %q", modPath)
	}
	file := strings.TrimSpace(string(out))
	if file == "" {
		return "", fmt.Errorf("cannot find target for tool %q", modPath)
	} else if _, err := os.Stat(file); err != nil {
		return "", fmt.Errorf("cannot stat %q for tool %q: %w", file, modPath, err)
	}
	return file, nil
}
