package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	_ "github.com/Antonboom/testifylint/analyzer"
	_ "github.com/BurntSushi/toml"
	_ "github.com/kisielk/errcheck/errcheck"
	_ "github.com/ldez/usetesting"
	_ "honnef.co/go/tools/staticcheck"

	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func main() {
	if err := newBuilder().run(); err != nil {
		log.Fatal(err)
	}
}

const coverageDir = ".build/coverage"
const defaultTestLogDir = ".build/test-logs"

const (
	testConsoleOutputFull     = "full"
	testConsoleOutputFailures = "failures"
)

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
	moduleDirs, err := b.checkModuleDirs()
	if err != nil {
		return fmt.Errorf("failed finding modules to check: %w", err)
	}

	// Run go vet
	if err := b.runCmdInDirs(moduleDirs, "go", "vet", "./..."); err != nil {
		return fmt.Errorf("go vet failed: %w", err)
	}
	// Run errcheck
	if errCheck, err := b.getInstalledTool("github.com/kisielk/errcheck"); err != nil {
		return fmt.Errorf("failed getting errcheck: %w", err)
	} else if err := b.runCmdInDirs(moduleDirs, errCheck, "./..."); err != nil {
		return fmt.Errorf("errcheck failed: %w", err)
	}
	// Run staticcheck
	if staticCheck, err := b.getInstalledTool("honnef.co/go/tools/cmd/staticcheck"); err != nil {
		return fmt.Errorf("failed getting staticcheck: %w", err)
	} else if err := b.runCmdInDirs(moduleDirs, staticCheck, "./..."); err != nil {
		return fmt.Errorf("staticcheck failed: %w", err)
	}
	// Run usetesting
	if useTesting, err := b.getInstalledTool("github.com/ldez/usetesting/cmd/usetesting"); err != nil {
		return fmt.Errorf("failed getting usetesting: %w", err)
	} else if err := b.runCmd(b.cmdFromRoot(
		useTesting,
		"-contextbackground",
		"-contexttodo",
		"-oschdir=false",
		"-oscreatetemp",
		"-osmkdirtemp",
		"-ossetenv",
		"-ostempdir",
		"./...",
	)); err != nil {
		return fmt.Errorf("usetesting failed: %w", err)
	}
	// Run correctness-oriented testifylint checks. Staticcheck remains the style baseline.
	if testifyLint, err := b.getInstalledTool("github.com/Antonboom/testifylint"); err != nil {
		return fmt.Errorf("failed getting testifylint: %w", err)
	} else if err := b.runCmd(b.cmdFromRoot(
		testifyLint,
		"-disable-all",
		"-enable=nil-compare,suite-broken-parallel,suite-method-signature,useless-assert",
		"./...",
	)); err != nil {
		return fmt.Errorf("testifylint failed: %w", err)
	}
	// Run doclink check
	if err := b.runCmd(b.cmdFromRoot("go", "run", "./internal/cmd/tools/doclink/doclink.go")); err != nil {
		return fmt.Errorf("doclink check failed: %w", err)
	}
	// Check SetupTest bindings for embedded require assertions in testify suites.
	testSuiteAssertions, err := b.getInstalledTool("go.temporal.io/sdk/internal/cmd/build/cmd/testsuiteassertions")
	if err != nil {
		return fmt.Errorf("failed getting testsuiteassertions: %w", err)
	}
	allModuleDirs, err := findModuleDirs(os.DirFS(b.rootDir))
	if err != nil {
		return fmt.Errorf("failed finding Go modules: %w", err)
	}
	for _, moduleDir := range allModuleDirs {
		cmd := b.cmdFromRoot(testSuiteAssertions, "./...")
		cmd.Dir = filepath.Join(b.rootDir, moduleDir)
		if err := b.runCmd(cmd); err != nil {
			return fmt.Errorf("testsuiteassertions check failed in %s: %w", moduleDir, err)
		}
	}
	return nil
}

func (b *builder) checkModuleDirs() ([]string, error) {
	moduleDirs := []string{b.rootDir}
	contribDir := filepath.Join(b.rootDir, "contrib")
	err := filepath.WalkDir(contribDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "go.mod" {
			moduleDirs = append(moduleDirs, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(moduleDirs[1:])
	return moduleDirs, nil
}

func (b *builder) runCmdInDirs(dirs []string, args ...string) error {
	for _, dir := range dirs {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if err := b.runCmd(cmd); err != nil {
			moduleDir, relErr := filepath.Rel(b.rootDir, dir)
			if relErr != nil {
				moduleDir = dir
			}
			return fmt.Errorf("failed in module %q: %w", moduleDir, err)
		}
	}
	return nil
}

func findModuleDirs(root fs.FS) ([]string, error) {
	var moduleDirs []string
	err := fs.WalkDir(root, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".build", ".git", "node_modules", "testdata", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			moduleDirs = append(moduleDirs, path.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(moduleDirs)
	return moduleDirs, nil
}

func (b *builder) integrationTest() error {
	// Supports some flags
	flagSet := flag.NewFlagSet("integration-test", flag.ContinueOnError)
	runFlag := flagSet.String("run", "", "Passed to go test as -run")
	pFlag := flagSet.String("p", "", "Passed to go test as -p")
	packagesFlag := flagSet.String("packages", "./...", "Packages passed to go test")
	devServerFlag := flagSet.Bool("dev-server", false, "Use an embedded dev server")
	envConfigFlag := flagSet.Bool("envconfig", false, "Load test server client options from envconfig")
	coverageFileFlag := flagSet.String("coverage-file", "", "If set, enables coverage output to this filename")
	testOutputFlags := addTestOutputFlags(flagSet)
	if err := flagSet.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed parsing flags: %w", err)
	}
	if *devServerFlag && *envConfigFlag {
		return fmt.Errorf("-dev-server and -envconfig cannot be used together")
	}
	testOutput, err := b.prepareTestOutput(*testOutputFlags, "go-test.log")
	if err != nil {
		return err
	}
	combinedLogPath, err := b.prepareLogPath(testOutputFlags.logDir, "combined.log")
	if err != nil {
		return err
	}
	combinedLog, err := os.OpenFile(combinedLogPath, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed opening combined test log %q: %w", combinedLogPath, err)
	}
	defer func() {
		if err := combinedLog.Close(); err != nil {
			log.Printf("Failed closing combined test log: %v", err)
		}
	}()
	testOutput.combinedLogPath = combinedLogPath
	testOutput.combinedWriter = &lockedWriter{writer: combinedLog}
	rerunArgs := []string{"go", "run", ".", "integration-test"}
	if *devServerFlag {
		rerunArgs = append(rerunArgs, "-dev-server")
	}
	if *envConfigFlag {
		rerunArgs = append(rerunArgs, "-envconfig")
	}
	if *pFlag != "" {
		rerunArgs = append(rerunArgs, "-p", *pFlag)
	}
	if *packagesFlag != "./..." {
		rerunArgs = append(rerunArgs, "-packages", *packagesFlag)
	}
	testOutput.rerunCommand = formatShellCommand(rerunArgs)

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
		devServerLogPath, err := b.prepareLogPath(testOutputFlags.logDir, "dev-server.log")
		if err != nil {
			return err
		}
		devServerLog, err := os.OpenFile(devServerLogPath, os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("failed opening dev server log %q: %w", devServerLogPath, err)
		}
		defer func() {
			if err := devServerLog.Close(); err != nil {
				log.Printf("Failed closing dev server log: %v", err)
			}
		}()
		testOutput.serverLogPath = devServerLogPath
		devServerLogWriter := &lockedWriter{writer: devServerLog}
		devServerStdout, devServerStderr := testOutput.writers(
			io.MultiWriter(devServerLogWriter, testOutput.combinedWriter),
			nil,
		)
		devServerLogger := sdklog.NewStructuredLogger(slog.New(slog.NewTextHandler(devServerStdout, nil)))
		devServer, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{
			CachedDownload: testsuite.CachedDownload{
				Version: "v1.8.3-server-1.32.0-162.0",
			},
			ClientOptions: &client.Options{
				HostPort:  "127.0.0.1:7233",
				Namespace: "integration-test-namespace",
				Logger:    devServerLogger,
			},
			DBFilename:       "temporal.sqlite",
			LogLevel:         "warn",
			SearchAttributes: searchAttributes,
			Stdout:           devServerStdout,
			Stderr:           devServerStderr,
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
				"--dynamic-config-value", `callback.allowedAddresses=[{"Pattern":"*","AllowInsecure":true}]`, // SDK tests use arbitrary callback URLs, permit that on the server
				"--dynamic-config-value", `system.refreshNexusEndpointsMinWait="0s"`, // Make Nexus tests faster
				"--dynamic-config-value", `component.nexusoperations.recordCancelRequestCompletionEvents=true`, // Defaults to false until after OSS 1.28 is released
				"--dynamic-config-value", `history.enableRequestIdRefLinks=true`,
				"--dynamic-config-value", `component.nexusoperations.useSystemCallbackURL=false`,
				"--dynamic-config-value", `component.nexusoperations.callback.endpoint.template="http://localhost:7243/namespaces/{{.NamespaceName}}/nexus/callback"`,
				"--dynamic-config-value", "nexusoperation.enableStandalone=true",
				"--dynamic-config-value", "history.enableCHASMCallbacks=true",
				"--dynamic-config-value", "frontend.ListWorkersEnabled=true",
				"--dynamic-config-value", "activity.startDelayEnabled=true",
				"--dynamic-config-value", "history.enableUpdateCallbacks=true",
				"--dynamic-config-value", "activity.enableCallbacks=true",
			},
		})
		if err != nil {
			startErr := fmt.Errorf("failed starting dev server: %w", err)
			if testOutput.consoleOutput == testConsoleOutputFailures {
				if reportErr := writeTestSetupFailureReport(testOutput.stderr, startErr, testOutput); reportErr != nil {
					log.Printf("Failed writing test setup failure report: %v", reportErr)
				}
			}
			return startErr
		}
		defer func() { _ = devServer.Stop() }()
	}

	// Run integration test
	args := []string{"go", "test", "-json", "-count", "1", "-race", "-v", "-timeout", "15m"}
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
	if *envConfigFlag {
		env = append(env, "TEMPORAL_TEST_ENV_CONFIG_SERVER=true")
	}
	// Must run in test dir
	cmd := b.cmdFromRoot(args...)
	cmd.Dir = filepath.Join(cmd.Dir, "test")
	cmd.Env = env
	if err := b.runTestCmd(cmd, testOutput); err != nil {
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
	testOutputFlags := addTestOutputFlags(flagSet)
	if err := flagSet.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed parsing flags: %w", err)
	}
	testOutput, err := b.prepareTestOutput(*testOutputFlags, "unit-test.log")
	if err != nil {
		return err
	}
	testOutput.rerunCommand = "go run . unit-test"

	// Find every non ./test-prefixed package that has a test file
	testDirMap := map[string]struct{}{}
	var testDirs []string
	err = fs.WalkDir(os.DirFS(b.rootDir), ".", func(p string, d fs.DirEntry, err error) error {
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
		args := []string{"go", "test", "-json", "-count", "1", "-race", "-v", "-timeout", "5m"}
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
		if err := b.runTestCmd(cmd, testOutput); err != nil {
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

type testOutputFlags struct {
	logDir        string
	consoleOutput string
}

func addTestOutputFlags(flagSet *flag.FlagSet) *testOutputFlags {
	var flags testOutputFlags
	flagSet.StringVar(
		&flags.logDir,
		"log-dir",
		defaultTestLogDir,
		"Directory for full test logs, relative to the repository root",
	)
	flagSet.StringVar(
		&flags.consoleOutput,
		"console-output",
		testConsoleOutputFailures,
		`Test output written to the console: "full" or "failures"`,
	)
	return &flags
}

type testOutput struct {
	logPath         string
	jsonLogPath     string
	combinedLogPath string
	serverLogPath   string
	rerunCommand    string
	combinedWriter  io.Writer
	consoleOutput   string
	stdout          io.Writer
	stderr          io.Writer
}

func (t testOutput) openLog() (*os.File, error) {
	f, err := os.OpenFile(t.logPath, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed opening test log %q: %w", t.logPath, err)
	}
	return f, nil
}

func (t testOutput) writers(logWriter io.Writer, capture io.Writer) (io.Writer, io.Writer) {
	var stdoutWriters, stderrWriters []io.Writer
	if t.consoleOutput == testConsoleOutputFull {
		stdoutWriters = append(stdoutWriters, t.stdout)
		stderrWriters = append(stderrWriters, t.stderr)
	}
	stdoutWriters = append(stdoutWriters, logWriter)
	stderrWriters = append(stderrWriters, logWriter)
	if capture != nil {
		stdoutWriters = append(stdoutWriters, capture)
		stderrWriters = append(stderrWriters, capture)
	}
	return io.MultiWriter(stdoutWriters...), io.MultiWriter(stderrWriters...)
}

func (b *builder) prepareTestOutput(flags testOutputFlags, logName string) (testOutput, error) {
	if strings.TrimSpace(flags.logDir) == "" {
		return testOutput{}, fmt.Errorf("-log-dir must not be empty")
	}
	switch flags.consoleOutput {
	case testConsoleOutputFull, testConsoleOutputFailures:
	default:
		return testOutput{}, fmt.Errorf(
			"invalid -console-output %q: must be %q or %q",
			flags.consoleOutput,
			testConsoleOutputFull,
			testConsoleOutputFailures,
		)
	}
	logPath, err := b.prepareLogPath(flags.logDir, logName)
	if err != nil {
		return testOutput{}, err
	}
	jsonLogName := strings.TrimSuffix(logName, filepath.Ext(logName)) + ".json"
	jsonLogPath, err := b.prepareLogPath(flags.logDir, jsonLogName)
	if err != nil {
		return testOutput{}, err
	}
	log.Printf("Writing full test logs to %v", filepath.Dir(logPath))
	return testOutput{
		logPath:       logPath,
		jsonLogPath:   jsonLogPath,
		consoleOutput: flags.consoleOutput,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
	}, nil
}

func (b *builder) prepareLogPath(logDirFlag, logName string) (string, error) {
	if strings.TrimSpace(logDirFlag) == "" {
		return "", fmt.Errorf("-log-dir must not be empty")
	}
	logDir := filepath.FromSlash(logDirFlag)
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(b.rootDir, logDir)
	}
	logDir = filepath.Clean(logDir)
	if err := os.MkdirAll(logDir, 0777); err != nil {
		return "", fmt.Errorf("failed creating test log directory %q: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, logName)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0666)
	if err != nil {
		return "", fmt.Errorf("failed preparing test log %q: %w", logPath, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("failed closing test log %q: %w", logPath, err)
	}
	return logPath, nil
}

// runTestCmd runs a go test command while saving full output and capturing
// structured results for the concise console report and GitHub job summary.
func (b *builder) runTestCmd(cmd *exec.Cmd, testOutput testOutput) error {
	logFile, err := testOutput.openLog()
	if err != nil {
		return err
	}
	jsonLogFile, err := os.OpenFile(testOutput.jsonLogPath, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("failed opening test JSON log %q: %w", testOutput.jsonLogPath, err)
	}

	logWriters := []io.Writer{&lockedWriter{writer: logFile}}
	if testOutput.combinedWriter != nil {
		logWriters = append(logWriters, testOutput.combinedWriter)
	}
	plainLogWriter := io.MultiWriter(logWriters...)
	stdoutWriter, stderrWriter := testOutput.writers(plainLogWriter, nil)
	results := newGoTestResults()
	jsonWriter := &goTestJSONWriter{
		rawWriter:    jsonLogFile,
		outputWriter: stdoutWriter,
		results:      results,
	}
	cmd.Stdout = jsonWriter
	cmd.Stderr = io.MultiWriter(stderrWriter, writerFunc(results.recordRawOutput))
	log.Printf("Running %v in %v with args %v", cmd.Path, cmd.Dir, cmd.Args[1:])
	if _, err := fmt.Fprintf(plainLogWriter, "Running %v in %v with args %v\n", cmd.Path, cmd.Dir, cmd.Args[1:]); err != nil {
		_ = logFile.Close()
		_ = jsonLogFile.Close()
		return fmt.Errorf("failed writing test log %q: %w", testOutput.logPath, err)
	}
	runErr := cmd.Run()
	flushErr := jsonWriter.Flush()
	logCloseErr := logFile.Close()
	jsonCloseErr := jsonLogFile.Close()
	rows := results.failures()
	if runErr != nil {
		summaryErr := appendTestFailureRows(os.Getenv("GITHUB_STEP_SUMMARY"), rows)
		if summaryErr != nil {
			log.Printf("Failed writing test failure summary: %v", summaryErr)
		}
		if testOutput.consoleOutput == testConsoleOutputFailures {
			reportErr := writeStructuredTestFailureReport(
				testOutput.stderr,
				rows,
				results.fallbackOutput(),
				testOutput,
			)
			if reportErr != nil {
				log.Printf("Failed writing test failure report: %v", reportErr)
			}
		}
		return runErr
	}
	if flushErr != nil {
		return fmt.Errorf("failed decoding test JSON output: %w", flushErr)
	}
	if logCloseErr != nil {
		return fmt.Errorf("failed closing test log %q: %w", testOutput.logPath, logCloseErr)
	}
	if jsonCloseErr != nil {
		return fmt.Errorf("failed closing test JSON log %q: %w", testOutput.jsonLogPath, jsonCloseErr)
	}
	return nil
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
