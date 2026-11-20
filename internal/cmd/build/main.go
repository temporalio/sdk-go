package main

import (
	"bytes"
	"context"
	"errors"
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
	"slices"
	"sort"
	"strings"
	"sync"

	_ "github.com/Antonboom/testifylint/analyzer"
	_ "github.com/BurntSushi/toml"
	_ "github.com/kisielk/errcheck/errcheck"
	_ "github.com/ldez/usetesting"
	"golang.org/x/mod/modfile"
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
const integrationTestModuleDir = "test"
const unitTestPackageConcurrency = "1"
const unitTestWorkers = 2

type unitCoverage int

const (
	unitCoverageDisabled unitCoverage = iota
	unitCoverageEnabled
)

var testFailureReportMu sync.Mutex

const (
	testConsoleOutputFull     = "full"
	testConsoleOutputFailures = "failures"
)

const usage = `Usage: go run . <command> [options]

Commands:
  check                 Run repository checks
  generate              Update or verify freshness of generated code
  unit-test             Run unit tests
  integration-test      Run integration tests
  test-release          Run release tests selected by PR labels
  merge-coverage-files  Merge coverage profiles

Run a command with "-h" or "-help" for usage. For example:
  go run . unit-test -help
`

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
		return fmt.Errorf("missing command name: use 'check', 'generate', 'integration-test', 'unit-test', 'test-release', or 'merge-coverage-files'")
	}
	switch os.Args[1] {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(os.Stdout, usage)
		return nil
	case "check":
		return b.check()
	case "generate":
		return b.generate(os.Args[2:])
	case "integration-test":
		return b.integrationTest(os.Args[2:], nil)
	case "merge-coverage-files":
		return b.mergeCoverageFiles()
	case "unit-test":
		return b.unitTest(os.Args[2:])
	case "test-release":
		return b.testRelease(os.Args[2:])
	default:
		return fmt.Errorf("unrecognized command %q: use 'check', 'generate', 'integration-test', 'unit-test', 'test-release', or 'merge-coverage-files'", os.Args[1])
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

func (b *builder) generate(args []string) error {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	check := flags.Bool("check", false, "check that generated files are up-to-date without modifying them")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate: unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	// Run payload limit generator
	cmd := []string{"go", "run", "./internal/cmd/payload-limit-gen", "-out", "internal/payloadlimits/validator_gen.go"}
	if *check {
		cmd = append(cmd, "-check")
	}
	if err := b.runCmd(b.cmdFromRoot(cmd...)); err != nil {
		return fmt.Errorf("payload limit validator: %w", err)
	}

	// Run nexgen, the System Nexus API generator
	cmd = []string{"go", "run", "./internal/cmd/system-nexus-gen"}
	if *check {
		cmd = append(cmd, "-check")
	}
	if err := b.runCmd(b.cmdFromRoot(cmd...)); err != nil {
		return fmt.Errorf("system nexus generator: %w", err)
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

func findUnitModuleDirs(root fs.FS) ([]string, error) {
	moduleDirs, err := findModuleDirs(root)
	if err != nil {
		return nil, err
	}

	// Integration tests have their own command and server setup.
	unitModuleDirs := make([]string, 0, len(moduleDirs))
	for _, moduleDir := range moduleDirs {
		if isIntegrationTestModuleDir(moduleDir) {
			continue
		}
		unitModuleDirs = append(unitModuleDirs, moduleDir)
	}
	return unitModuleDirs, nil
}

func isIntegrationTestModuleDir(moduleDir string) bool {
	if moduleDir == integrationTestModuleDir || strings.HasPrefix(moduleDir, integrationTestModuleDir+"/") {
		return true
	}
	if !strings.HasPrefix(moduleDir, "contrib/") {
		return false
	}
	for dir := moduleDir; dir != "."; dir = path.Dir(dir) {
		if path.Base(dir) == integrationTestModuleDir {
			return true
		}
	}
	return false
}

const integrationTestUsage = `Usage: go run . integration-test [options]

Examples:
  go run . integration-test
  go run . integration-test -module test -dev-server -run ^TestSelected$
  go run . integration-test -module contrib/example/test,contrib/other/test
  go run . integration-test -module test,contrib/example/test -release-modules contrib/example

Options:
`

func (b *builder) integrationTest(args []string, extraEnv []string) error {
	// Supports some flags
	flagSet := flag.NewFlagSet("integration-test", flag.ContinueOnError)
	flagSet.Usage = func() {
		fmt.Fprint(flagSet.Output(), integrationTestUsage)
		flagSet.PrintDefaults()
	}
	var testModules, releaseModules cliList
	flagSet.Var(&testModules, "module", "Run only these integration test modules, given as a comma-separated list (default: all)")
	flagSet.Var(&releaseModules, "release-modules", "Ignore all go.mod replacements except the given modules, given as a comma-separated list")
	runFlag := flagSet.String("run", "", "Passed to go test as -run")
	pFlag := flagSet.String("p", "", "Passed to go test as -p")
	packagesFlag := flagSet.String("packages", "./...", "Packages passed to go test")
	devServerFlag := flagSet.Bool("dev-server", false, "Use an embedded dev server")
	envConfigFlag := flagSet.Bool("envconfig", false, "Load test server client options from envconfig")
	cloudFlag := flagSet.Bool("cloud", false, "Run tests in Temporal Cloud mode")
	coverageFileFlag := flagSet.String("coverage-file", "", "If set, enables coverage output to this filename")
	testOutputFlags := addTestOutputFlags(flagSet)
	timeoutFlag := flagSet.String("timeout", "15m", "Passed to go test as -timeout")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("failed parsing flags: %w", err)
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("integration-test: unexpected arguments: %s", strings.Join(flagSet.Args(), " "))
	}
	if *devServerFlag && *envConfigFlag {
		return fmt.Errorf("-dev-server and -envconfig cannot be used together")
	}
	if *cloudFlag && !*envConfigFlag {
		return fmt.Errorf("-cloud requires -envconfig")
	}
	moduleDirs, err := findModuleDirs(os.DirFS(b.rootDir))
	if err != nil {
		return fmt.Errorf("failed finding integration test modules: %w", err)
	}
	for _, name := range releaseModules {
		if !slices.Contains(moduleDirs, name) || isIntegrationTestModuleDir(name) {
			return fmt.Errorf("argument %q of -release-modules is not a releasable module", name)
		}
	}
	for _, name := range testModules {
		if !slices.Contains(moduleDirs, name) || !isIntegrationTestModuleDir(name) {
			return fmt.Errorf("argument %q of -module is not an integration test module", name)
		}
	}
	integrationModuleDirs := make([]string, 0, len(moduleDirs))
	for _, moduleDir := range moduleDirs {
		if isIntegrationTestModuleDir(moduleDir) && (len(testModules) == 0 || slices.Contains(testModules, moduleDir)) {
			integrationModuleDirs = append(integrationModuleDirs, moduleDir)
		}
	}
	if len(integrationModuleDirs) == 0 {
		return fmt.Errorf("no integration test modules found matching %q; expected test/ modules at the repository root or under contrib/", strings.Join(testModules, ","))
	}
	for _, moduleDir := range integrationModuleDirs {
		if (*envConfigFlag || *cloudFlag) && moduleDir != integrationTestModuleDir {
			return fmt.Errorf("integration test module %q manages its own server and does not support -envconfig or -cloud", moduleDir)
		}
	}
	output, err := b.prepareTestOutput(*testOutputFlags, "go-test.log")
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
	output.combinedLogPath = combinedLogPath
	output.combinedWriter = &lockedWriter{writer: combinedLog}

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

	runModule := func(moduleDir string, testOutput testOutput) (retErr error) {
		rootModule := moduleDir == integrationTestModuleDir
		rerunArgs := []string{"go", "run", ".", "integration-test"}
		rerunArgs = append(rerunArgs, "-module", moduleDir)
		if releaseModules != nil {
			rerunArgs = append(rerunArgs, "-release-modules", releaseModules.String())
		}
		if *devServerFlag && rootModule {
			rerunArgs = append(rerunArgs, "-dev-server")
		}
		if *envConfigFlag && rootModule {
			rerunArgs = append(rerunArgs, "-envconfig")
		}
		if *cloudFlag && rootModule {
			rerunArgs = append(rerunArgs, "-cloud")
		}
		if *pFlag != "" {
			rerunArgs = append(rerunArgs, "-p", *pFlag)
		}
		if *packagesFlag != "./..." {
			rerunArgs = append(rerunArgs, "-packages", *packagesFlag)
		}
		if *timeoutFlag != "15m" {
			rerunArgs = append(rerunArgs, "-timeout", *timeoutFlag)
		}
		testOutput.rerunCommand = formatShellCommand(rerunArgs)

		var modFile string
		if releaseModules != nil {
			tempDir, err := os.MkdirTemp(filepath.Dir(testOutput.logPath), ".release-")
			if err != nil {
				return fmt.Errorf("creating release manifest directory: %w", err)
			}
			defer func() {
				if err := os.RemoveAll(tempDir); err != nil {
					retErr = errors.Join(retErr, fmt.Errorf("removing release manifest directory: %w", err))
				}
			}()
			modFile, err = b.prepareReleaseManifest(
				filepath.Join(b.rootDir, filepath.FromSlash(moduleDir)), tempDir, releaseModules)
			if err != nil {
				return err
			}
		}

		customKeyField := temporal.NewSearchAttributeKeyKeyword("CustomKeywordField")
		customStringField := temporal.NewSearchAttributeKeyString("CustomStringField")
		searchAttributes := temporal.NewSearchAttributes(
			customKeyField.ValueSet("Keyword"),
			customStringField.ValueSet("Text"),
		)

		// Start dev server if wanted
		if *devServerFlag && rootModule {
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
					Version: "v1.9.3-nexus-serialization-context",
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
					// Matching fan-out poll cancellation can drop a query dispatched while a stopped worker's polls are canceled
					"--dynamic-config-value", "frontend.enableMatchingFanOutForPollCancellation=false",
					"--http-port", "7243", // Nexus tests use the HTTP port directly
					"--dynamic-config-value", `callback.allowedAddresses=[{"Pattern":"*","AllowInsecure":true}]`, // SDK tests use arbitrary callback URLs, permit that on the server
					"--dynamic-config-value", `system.refreshNexusEndpointsMinWait="0s"`, // Make Nexus tests faster
					"--dynamic-config-value", `component.nexusoperations.recordCancelRequestCompletionEvents=true`, // Defaults to false until after OSS 1.28 is released
					"--dynamic-config-value", `history.enableRequestIdRefLinks=true`,
					"--dynamic-config-value", `component.nexusoperations.useSystemCallbackURL=false`,
					"--dynamic-config-value", `component.nexusoperations.callback.endpoint.template="http://localhost:7243/namespaces/{{.NamespaceName}}/nexus/callback"`,
					"--dynamic-config-value", "nexusoperation.enableStandalone=true",
					"--dynamic-config-value", "history.enableChasm=true",
					"--dynamic-config-value", "history.enableSignalWithStartFromWorkflow=true",
					"--dynamic-config-value", "history.enableCHASMCallbacks=true",
					"--dynamic-config-value", "frontend.ListWorkersEnabled=true",
					"--dynamic-config-value", "activity.startDelayEnabled=true",
					"--dynamic-config-value", "history.enableUpdateCallbacks=true",
					"--dynamic-config-value", "activity.enableCallbacks=true",
					"--dynamic-config-value", "history.enableWorkflowTaskCompletionPagination=true",
					// Pagination clears the gRPC request limit, but the recombined completion still
					// persists as one transaction, so raise the persistence limit above it.
					"--dynamic-config-value", "system.transactionSizeLimit=33554432",
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
		args := []string{"go", "test", "-json", "-count", "1", "-race", "-v", "-timeout", *timeoutFlag}
		env := append(os.Environ(), "DISABLE_SERVER_1_25_TESTS=1")
		env = append(env, extraEnv...)
		if releaseModules != nil {
			args = append(args, "-mod=readonly", "-modfile="+modFile)
			env = append(env, "GOWORK=off")
		}
		if *runFlag != "" {
			args = append(args, "-run", *runFlag)
		}
		if *pFlag != "" {
			args = append(args, "-p", *pFlag)
		}
		if *coverageFileFlag != "" {
			coverageFile := *coverageFileFlag
			if !rootModule {
				coverageFile = filepath.Join(filepath.Dir(coverageFile), strings.ReplaceAll(moduleDir, "/", "-")+"-"+filepath.Base(coverageFile))
			}
			args = append(args, "-coverprofile="+filepath.Join(b.rootDir, coverageDir, coverageFile), "-coverpkg=./...")
		}
		args = append(args, strings.Fields(*packagesFlag)...)
		if *devServerFlag && rootModule {
			args = append(args, "--", "-using-cli-dev-server")
			env = append(env, "TEMPORAL_NAMESPACE=integration-test-namespace")
		}
		if *envConfigFlag && rootModule {
			env = append(env, "TEMPORAL_TEST_ENV_CONFIG_SERVER=true")
		}
		if *cloudFlag && rootModule {
			env = append(env, "TEMPORAL_IS_CLOUD_TESTS=true")
		}
		cmd := b.cmdFromRoot(args...)
		cmd.Dir = filepath.Join(cmd.Dir, filepath.FromSlash(moduleDir))
		cmd.Env = env
		return b.runTestCmd(cmd, testOutput)
	}
	var failures []error
	for _, moduleDir := range integrationModuleDirs {
		if err := runModule(moduleDir, output); err != nil {
			failures = append(failures, fmt.Errorf("integration test module %q: %w", moduleDir, err))
		}
	}
	return errors.Join(failures...)
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

const unitTestUsage = `Usage: go run . unit-test [options]

Examples:
  go run . unit-test
  go run . unit-test -module contrib/example/test -run ^TestSelected$
  go run . unit-test -module contrib/example,contrib/other -release-modules contrib/example,contrib/other

Options:
`

func (b *builder) unitTest(args []string) error {
	flagSet := flag.NewFlagSet("unit-test", flag.ContinueOnError)
	flagSet.Usage = func() {
		fmt.Fprint(flagSet.Output(), unitTestUsage)
		flagSet.PrintDefaults()
	}
	var testModules, releaseModules cliList
	flagSet.Var(&testModules, "module", "Comma-separated unit test module directories relative to the repository root (default: all)")
	flagSet.Var(&releaseModules, "release-modules", "Ignore all go.mod replacements except the given modules, given as a comma-separated list")
	runFlag := flagSet.String("run", "", "Passed to go test as -run")
	coverageFlag := flagSet.Bool("coverage", false, "If set, enables coverage output")
	testOutputFlags := addTestOutputFlags(flagSet)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("failed parsing flags: %w", err)
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("unit-test: unexpected arguments: %s", strings.Join(flagSet.Args(), " "))
	}

	moduleNames, err := findUnitModuleDirs(os.DirFS(b.rootDir))
	if err != nil {
		return fmt.Errorf("failed finding modules to test: %w", err)
	}
	for _, name := range releaseModules {
		if !slices.Contains(moduleNames, name) || isIntegrationTestModuleDir(name) {
			return fmt.Errorf("argument %q of -release-modules is not a releasable module", name)
		}
	}
	for _, name := range testModules {
		if !slices.Contains(moduleNames, name) {
			return fmt.Errorf("argument %q of -module has no unit tests", name)
		}
	}
	moduleDirs := make([]string, 0, len(moduleNames))
	for _, moduleName := range moduleNames {
		if len(testModules) == 0 || slices.Contains(testModules, moduleName) {
			moduleDirs = append(moduleDirs, filepath.Join(b.rootDir, filepath.FromSlash(moduleName)))
		}
	}
	if len(moduleDirs) == 0 {
		return fmt.Errorf("no unit test modules found")
	}
	testOutput, err := b.prepareTestOutput(*testOutputFlags, "unit-test.log")
	if err != nil {
		return err
	}

	// Create coverage dir if doing coverage
	if *coverageFlag {
		if err := os.MkdirAll(filepath.Join(b.rootDir, coverageDir), 0777); err != nil {
			return fmt.Errorf("failed creating coverage dir: %w", err)
		}
	}

	// Run modules concurrently while keeping package output sequential.
	log.Printf("Running unit tests in modules with %d workers: %v", unitTestWorkers, moduleDirs)
	coverage := unitCoverageDisabled
	if *coverageFlag {
		coverage = unitCoverageEnabled
	}
	return b.runUnitModules(moduleDirs, *runFlag, coverage, releaseModules, testOutput)
}

type unitWorker struct {
	output   testOutput
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	failures []unitFailure
}

type unitFailure struct {
	moduleDir string
	err       error
}

func (b *builder) runUnitModules(
	moduleDirs []string,
	run string,
	coverage unitCoverage,
	releaseModules []string,
	output testOutput,
) error {
	tempDir, err := os.MkdirTemp(filepath.Dir(output.logPath), ".unit-test-")
	if err != nil {
		return fmt.Errorf("failed creating unit test log directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			log.Printf("Failed removing temporary unit test logs: %v", err)
		}
	}()

	workers := make([]unitWorker, unitTestWorkers)
	for i := range workers {
		if err := b.prepareUnitWorker(tempDir, i, output, &workers[i]); err != nil {
			return err
		}
	}

	jobs := make(chan string)
	var wait sync.WaitGroup
	for i := range workers {
		wait.Add(1)
		go func(worker *unitWorker) {
			defer wait.Done()
			for moduleDir := range jobs {
				if err := b.runUnitModule(moduleDir, run, coverage, releaseModules, worker.output); err != nil {
					worker.failures = append(worker.failures, unitFailure{moduleDir: moduleDir, err: err})
				}
			}
		}(&workers[i])
	}
	for _, moduleDir := range moduleDirs {
		jobs <- moduleDir
	}
	close(jobs)
	wait.Wait()

	for i := range workers {
		if err := mergeWorkerOutput(output, &workers[i]); err != nil {
			return err
		}
	}
	for i := range workers {
		if len(workers[i].failures) > 0 {
			failure := workers[i].failures[0]
			return fmt.Errorf("unit test failed in %v: %w", failure.moduleDir, failure.err)
		}
	}
	return nil
}

func (b *builder) prepareUnitWorker(tempDir string, index int, output testOutput, worker *unitWorker) error {
	prefix := filepath.Join(tempDir, fmt.Sprintf("%03d", index))
	workerOutput := output
	workerOutput.logPath = prefix + ".log"
	workerOutput.jsonLogPath = prefix + ".json"
	for _, path := range []string{workerOutput.logPath, workerOutput.jsonLogPath} {
		file, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("failed preparing worker output %q: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("failed closing worker output %q: %w", path, err)
		}
	}
	workerOutput.stdout = &worker.stdout
	workerOutput.stderr = &worker.stderr
	worker.output = workerOutput
	return nil
}

func (b *builder) runUnitModule(moduleDir string, run string, coverage unitCoverage, releaseModules []string, output testOutput) (retErr error) {
	moduleName, err := filepath.Rel(b.rootDir, moduleDir)
	if err != nil {
		return fmt.Errorf("resolving to relative path %q: %w", moduleDir, err)
	}
	rerunArgs := []string{"go", "run", ".", "unit-test", "-module", filepath.ToSlash(moduleName)}
	if releaseModules != nil {
		rerunArgs = append(rerunArgs, "-release-modules", strings.Join(releaseModules, ","))
	}
	output.rerunCommand = formatShellCommand(rerunArgs)

	args := []string{
		"go", "test", "-json", "-count", "1", "-race", "-v", "-timeout", "5m",
		"-p", unitTestPackageConcurrency,
	}
	if releaseModules != nil {
		tempDir, err := os.MkdirTemp(filepath.Dir(output.logPath), ".release-")
		if err != nil {
			return fmt.Errorf("creating release manifest directory: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("removing release manifest directory: %w", err))
			}
		}()
		modFile, err := b.prepareReleaseManifest(moduleDir, tempDir, releaseModules)
		if err != nil {
			return err
		}
		args = append(args, "-mod=readonly", "-modfile="+modFile)
	}
	if run != "" {
		args = append(args, "-run", run)
	}
	if coverage == unitCoverageEnabled {
		if moduleName == "." {
			moduleName = "root"
		}
		moduleName = strings.ReplaceAll(moduleName, string(filepath.Separator), "-")
		coverageFile := filepath.Join(b.rootDir, coverageDir, "unit-test-"+moduleName+".out")
		args = append(args, "-coverprofile="+coverageFile, "-coverpkg=./...")
	}
	args = append(args, "./...")

	cmd := b.cmdFromRoot(args...)
	cmd.Dir = moduleDir
	if releaseModules != nil {
		cmd.Env = append(os.Environ(), "GOWORK=off")
	}
	return b.runTestCmd(cmd, output)
}

func mergeWorkerOutput(output testOutput, worker *unitWorker) error {
	for _, pair := range [][2]string{
		{output.logPath, worker.output.logPath},
		{output.jsonLogPath, worker.output.jsonLogPath},
	} {
		if err := appendFile(pair[0], pair[1]); err != nil {
			return err
		}
	}
	if _, err := worker.stdout.WriteTo(output.stdout); err != nil {
		return fmt.Errorf("failed writing worker stdout: %w", err)
	}
	if _, err := worker.stderr.WriteTo(output.stderr); err != nil {
		return fmt.Errorf("failed writing worker stderr: %w", err)
	}
	return nil
}

func appendFile(destination, source string) error {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed opening merged test log %q: %w", destination, err)
	}
	defer file.Close()
	return writeFile(file, source)
}

func writeFile(writer io.Writer, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed opening module output %q: %w", source, err)
	}
	defer file.Close()
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("failed merging module output %q: %w", source, err)
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
	finalLogPath    string
	finalJSONPath   string
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
		finalLogPath:  logPath,
		finalJSONPath: jsonLogPath,
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
		testFailureReportMu.Lock()
		defer testFailureReportMu.Unlock()
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

// -- RELEASE MODE HELPERS ---------------------------------------

const testReleaseUsage = `Usage: go run . test-release -labels LABELS [options]

Runs release unit tests and each selected module's own integration tests
with and without the workflow cache.

Examples:
  go run . test-release -labels "Release: sdk"
  go run . test-release -labels "Release: contrib/envconfig,Release: contrib/opentelemetry-v2"

Options:
`

func (b *builder) testRelease(args []string) error {
	flags := flag.NewFlagSet("test-release", flag.ContinueOnError)
	labelsFlag := flags.String("labels", "", "Comma-separated PR labels, such as Release: sdk,Release: contrib/envconfig")
	runFlag := flags.String("run", "", "Passed to unit-test and integration-test as -run")
	testOutputFlags := addTestOutputFlags(flags)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), testReleaseUsage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *labelsFlag == "" || flags.NArg() != 0 {
		return fmt.Errorf("test-release requires -labels LABELS and no positional arguments")
	}

	// Parse labels in the GitHub PR to find our what modules we're releasing
	moduleDirs, err := findModuleDirs(os.DirFS(b.rootDir))
	if err != nil {
		return fmt.Errorf("finding release test modules: %w", err)
	}
	var releaseModules []string
	for _, label := range strings.Split(*labelsFlag, ",") {
		label = strings.TrimSpace(label)
		name, ok := strings.CutPrefix(label, "Release: ")
		if !ok {
			continue
		}
		moduleDir := name
		if name == "sdk" {
			moduleDir = "."
		}
		if (name != "sdk" && !strings.HasPrefix(name, "contrib/")) ||
			!slices.Contains(moduleDirs, moduleDir) || isIntegrationTestModuleDir(moduleDir) {
			return fmt.Errorf("unknown release module in label %q", label)
		}
		if !slices.Contains(releaseModules, moduleDir) {
			releaseModules = append(releaseModules, moduleDir)
		}
	}
	if len(releaseModules) == 0 {
		return fmt.Errorf("no release modules selected: expected at least one %q label", "Release: MODULE")
	}
	slices.Sort(releaseModules)

	// Run unit tests
	releases := strings.Join(releaseModules, ",")
	unitArgs := []string{
		"-module", releases,
		"-release-modules", releases,
		"-log-dir", filepath.Join(testOutputFlags.logDir, "unit"),
		"-console-output", testOutputFlags.consoleOutput,
	}
	if *runFlag != "" {
		unitArgs = append(unitArgs, "-run", *runFlag)
	}
	if err := b.unitTest(unitArgs); err != nil {
		return fmt.Errorf("release unit tests: %w", err)
	}

	// Run integration tests
	var integrationModules []string
	for _, moduleDir := range releaseModules {
		testDir := path.Join(moduleDir, integrationTestModuleDir)
		found := false
		for _, candidate := range moduleDirs {
			if candidate == testDir || strings.HasPrefix(candidate, testDir+"/") {
				integrationModules = append(integrationModules, candidate)
				found = true
			}
		}
		if !found {
			log.Printf("no integration tests found for %s", moduleDir)
		}
	}
	if len(integrationModules) == 0 {
		return nil
	}
	integrationArgs := []string{
		"-dev-server",
		"-module", strings.Join(integrationModules, ","),
		"-release-modules", releases,
		"-console-output", testOutputFlags.consoleOutput,
	}
	if *runFlag != "" {
		integrationArgs = append(integrationArgs, "-run", *runFlag)
	}
	if err := b.integrationTest(
		append(integrationArgs, "-log-dir", filepath.Join(testOutputFlags.logDir, "integration-cache")),
		[]string{"WORKFLOW_CACHE_SIZE="},
	); err != nil {
		return fmt.Errorf("release integration tests with cache: %w", err)
	}
	if err := b.integrationTest(
		append(integrationArgs, "-log-dir", filepath.Join(testOutputFlags.logDir, "integration-no-cache")),
		[]string{"WORKFLOW_CACHE_SIZE=0"},
	); err != nil {
		return fmt.Errorf("release integration tests without cache: %w", err)
	}
	return nil
}

// prepareReleaseManifest parses the module's go.mod and go.sum files
// and writes replacement files release.mod, release.sum to tempDir.
// The replacement files pin the module's dependencies to their
// published versions, except for the given releaseModules, which are
// replaced with local paths. Returns the path to release.mod.
func (b *builder) prepareReleaseManifest(moduleDir, tempDir string, releaseModules []string) (string, error) {
	sourcePath := filepath.Join(moduleDir, "go.mod")
	contents, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("reading %q: %w", sourcePath, err)
	}
	module, err := modfile.Parse(sourcePath, contents, nil)
	if err != nil {
		return "", fmt.Errorf("parsing %q: %w", sourcePath, err)
	}
	for _, replacement := range module.Replace {
		if err := module.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
			return "", fmt.Errorf("removing go.mod replace directive for %q: %w", replacement.Old.Path, err)
		}
	}
	for _, moduleName := range releaseModules {
		releaseModuleDir, err := filepath.Abs(filepath.Join(b.rootDir, filepath.FromSlash(moduleName)))
		if err != nil {
			return "", fmt.Errorf("constructing absolute release module path: %w", err)
		}
		releasePath := filepath.Join(releaseModuleDir, "go.mod")
		releaseContents, err := os.ReadFile(releasePath)
		if err != nil {
			return "", fmt.Errorf("reading %q: %w", releasePath, err)
		}
		releaseModule, err := modfile.Parse(releasePath, releaseContents, nil)
		if err != nil {
			return "", fmt.Errorf("parsing %q: %w", releasePath, err)
		}
		if releaseModule.Module == nil {
			return "", fmt.Errorf("missing module directive in %q", releasePath)
		}
		releaseModulePath := releaseModule.Module.Mod.Path
		if module.Module != nil && module.Module.Mod.Path == releaseModulePath {
			continue
		}
		if err := module.AddReplace(releaseModulePath, "", releaseModuleDir, ""); err != nil {
			return "", fmt.Errorf("adding local replacement for release module %q: %w", releaseModulePath, err)
		}
	}
	module.Cleanup()
	contents, err = module.Format()
	if err != nil {
		return "", fmt.Errorf("formatting %q: %w", sourcePath, err)
	}
	modPath, err := filepath.Abs(filepath.Join(tempDir, "release.mod"))
	if err != nil {
		return "", fmt.Errorf("constructing absolute path: %w", err)
	}
	if err := os.WriteFile(modPath, contents, 0666); err != nil {
		return "", fmt.Errorf("writing %q: %w", modPath, err)
	}
	sumPath := filepath.Join(moduleDir, "go.sum")
	sums, err := os.ReadFile(sumPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("reading %q: %w", sumPath, err)
	}
	if err == nil {
		dest := filepath.Join(filepath.Dir(modPath), "release.sum")
		if err := os.WriteFile(dest, sums, 0666); err != nil {
			return "", fmt.Errorf("writing %q: %w", dest, err)
		}
	}

	for _, args := range [][]string{
		{"go", "list", "-mod=mod", "-modfile=" + modPath, "-m", "all"},
		{"go", "mod", "download", "-modfile=" + modPath, "all"},
	} {
		cmd := b.cmdFromRoot(args...)
		cmd.Dir = moduleDir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf(
				"testing %q in release mode: ignoring replace directives and fetching declared dependencies: %w\n%s",
				moduleDir, err, strings.TrimSpace(string(output)))
		}
	}
	return modPath, nil
}

// -- CLI HELPERS ---------------------------------------

// cliList represents a comma-separated list of strings, passed as a command line
// argument.
type cliList []string

func (m cliList) String() string {
	return strings.Join(m, ",")
}

func (m *cliList) Set(value string) error {
	names := make([]string, 0)
	if value != "" {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("must be a comma-separated list without empty entries")
			}
			name = filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	*m = names
	return nil
}
