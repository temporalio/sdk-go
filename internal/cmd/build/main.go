package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	_ "github.com/BurntSushi/toml"
	_ "github.com/kisielk/errcheck/errcheck"
	_ "honnef.co/go/tools/staticcheck"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func main() {
	if err := newBuilder().run(); err != nil {
		log.Fatal(err)
	}
}

const coverageDir = ".build/coverage"

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
				Version: "v1.6.1-server-1.31.0-151.0",
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
	if *coverageFileFlag != "" {
		args = append(args, "-coverprofile="+filepath.Join(b.rootDir, coverageDir, *coverageFileFlag), "-coverpkg=./...")
	}
	args = append(args, "./...")
	if *devServerFlag {
		args = append(args, "--", "-using-cli-dev-server")
		env = append(env, "TEMPORAL_NAMESPACE=integration-test-namespace")
	}
	// Must run in test dir
	cmd := b.cmdFromRoot(args...)
	cmd.Dir = filepath.Join(cmd.Dir, "test")
	cmd.Env = env
	if err := b.runCmd(cmd); err != nil {
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
		if err := b.runCmd(cmd); err != nil {
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
