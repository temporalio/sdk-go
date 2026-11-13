// Command system-nexus-gen uses nexgen to generate the System Nexus API.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	workflowservice "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// options holds parsed command line flags
type options struct {
	check            bool
	preserveBuildDir bool
}

func newOptions(args []string) (options, error) {
	var options options

	flags := flag.NewFlagSet("system-nexus-gen", flag.ContinueOnError)
	flags.BoolVar(&options.check, "check", false, "check bindings without modifying the output files")
	flags.BoolVar(&options.preserveBuildDir, "preserve-build-dir", false, "preserve the build directory, even if the command succeeds")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return options, nil
}

// sdkDir is a path to the SDK root directory
type sdkDir string

func newSDKDir() (sdkDir, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate system-nexus-gen source file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	return sdkDir(root), nil
}

// apiDir is a path to the go.temporal.io/api module directory
type apiDir string

func newAPIDir(sdk sdkDir) (apiDir, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "go.temporal.io/api")
	cmd.Dir = string(sdk)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("locating go.temporal.io/api with go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dir := strings.TrimSpace(string(output))
	if dir == "" {
		return "", fmt.Errorf("go list returned no directory for go.temporal.io/api")
	}
	return apiDir(dir), nil
}

func (d apiDir) witDir() string {
	return filepath.Join(string(d), "systemnexus")
}

// buildDir is a path to the temporary build directory
type buildDir string

func newBuildDir(sdk sdkDir) (buildDir, error) {
	tmp, err := os.MkdirTemp(string(sdk), ".system-nexus-gen-")
	if err != nil {
		return "", fmt.Errorf("creating build dir: %w", err)
	}
	return buildDir(tmp), nil
}

func (d buildDir) witDir() string {
	return filepath.Join(string(d), "wit")
}

func (d buildDir) witFile() string {
	return filepath.Join(d.witDir(), filepath.FromSlash("workflowservice/v1/workflow-service.wit"))
}

func (d buildDir) witDepsDir() string {
	return filepath.Join(d.witDir(), "deps")
}

func (d buildDir) descriptorsDir() string {
	return filepath.Join(string(d), "descriptors")
}

func (d buildDir) descriptorsFile() string {
	return filepath.Join(d.descriptorsDir(), "descriptors.bin")
}

func (d buildDir) generatedDir() string {
	return filepath.Join(string(d), "generated")
}

// app represents the state of the system-nexus-gen command
type app struct {
	options options
	sdk     sdkDir
	api     apiDir
	tmp     buildDir
}

func newApp(options options) (app, error) {
	sdk, err := newSDKDir()
	if err != nil {
		return app{}, err
	}

	api, err := newAPIDir(sdk)
	if err != nil {
		return app{}, err
	}

	tmp, err := newBuildDir(sdk)
	if err != nil {
		return app{}, err
	}
	return app{options: options, sdk: sdk, api: api, tmp: tmp}, nil
}

func (a app) cleanup(runErr error) error {
	if a.tmp == "" {
		return nil
	}
	if a.options.preserveBuildDir || runErr != nil {
		log.Printf("Preserved build directory: %s", a.tmp)
		return nil
	}
	if err := os.RemoveAll(string(a.tmp)); err != nil {
		return fmt.Errorf("removing build directory %s: %w", a.tmp, err)
	}
	return nil
}

// -----------------------------------------------------------------------------
// LOGIC
// -----------------------------------------------------------------------------

func main() {
	log.SetFlags(0)
	options, err := newOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatalf("system-nexus-gen: %v", err)
	}
	app, err := newApp(options)
	if err != nil {
		log.Fatalf("system-nexus-gen: %v", err)
	}
	if err = app.run(); err != nil {
		log.Fatalf("system-nexus-gen: %v", err)
	}
}

func (a app) run() (err error) {
	defer func() {
		cleanupErr := a.cleanup(err)
		err = errors.Join(err, cleanupErr)
	}()

	if err := a.stageWitFiles(); err != nil {
		return fmt.Errorf("staging WIT files: %w", err)
	}

	if err := a.genProtoDescriptors(); err != nil {
		return fmt.Errorf("generating proto descriptors: %w", err)
	}

	if err := nexgen(nexgenOptions{
		language:       "go",
		inputs:         []string{a.tmp.witFile(), a.tmp.witDepsDir()},
		descriptors:    []string{a.tmp.descriptorsFile()},
		supportPackage: "go.temporal.io/sdk/internal",
		output:         a.tmp.generatedDir(),
		systemNexus:    true,
		format:         true,
	}); err != nil {
		return fmt.Errorf("nexgen: %w", err)
	}

	return a.updateGeneratedFiles()
}

func (a app) updateGeneratedFiles() error {
	for _, file := range []struct{ generated, destination string }{
		{"workflowservice.go", "internal/workflowservice.go"},
		{"workflowservice_facade.go", "workflow/workflowservice.go"},
	} {
		generatedFile := filepath.Join(a.tmp.generatedDir(), file.generated)
		checkedInFile := filepath.Join(string(a.sdk), filepath.FromSlash(file.destination))
		generated, err := os.ReadFile(generatedFile)
		if err != nil {
			return fmt.Errorf("reading generated binding %s: %w", generatedFile, err)
		}
		if a.options.check {
			checkedIn, err := os.ReadFile(checkedInFile)
			if err != nil {
				return fmt.Errorf("reading checked-in binding %s: %w", checkedInFile, err)
			}
			if !checkEqual(generated, checkedIn) {
				return fmt.Errorf("%s is out of date", checkedInFile)
			}
		} else {
			if err := os.WriteFile(checkedInFile, generated, 0o644); err != nil {
				return fmt.Errorf("writing binding %s: %w", checkedInFile, err)
			}
		}
	}

	return nil
}

func (a app) stageWitFiles() error {
	err := os.CopyFS(a.tmp.witDir(), os.DirFS(a.api.witDir()))
	if err != nil {
		return fmt.Errorf("copying from api-go: %w", err)
	}
	return nil
}

// genProtoDescriptors generates a proto descriptor set for the workflowservice API.
// Equivalent to running protoc with --include_imports.
func (a app) genProtoDescriptors() error {
	outputFile := a.tmp.descriptorsFile()

	if err := os.MkdirAll(filepath.Dir(outputFile), 0o700); err != nil {
		return fmt.Errorf("creating descriptor build directory: %w", err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]struct{})
	addProtoDescriptor(set, seen, workflowservice.File_temporal_api_workflowservice_v1_request_response_proto)

	contents, err := proto.Marshal(set)
	if err != nil {
		return fmt.Errorf("marshaling Temporal API descriptors: %w", err)
	}
	if err := os.WriteFile(outputFile, contents, 0o600); err != nil {
		return fmt.Errorf("writing Temporal API descriptors %s: %w", outputFile, err)
	}
	return nil
}

func addProtoDescriptor(set *descriptorpb.FileDescriptorSet, seen map[string]struct{}, file protoreflect.FileDescriptor) {
	if _, ok := seen[file.Path()]; ok {
		return
	}
	seen[file.Path()] = struct{}{}
	imports := file.Imports()
	for i := range imports.Len() {
		addProtoDescriptor(set, seen, imports.Get(i))
	}
	set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
}

type nexgenOptions struct {
	language       string
	inputs         []string
	descriptors    []string
	supportPackage string
	output         string
	systemNexus    bool
	format         bool
}

func nexgen(options nexgenOptions) error {
	nexGenExe, err := exec.LookPath("nexgen")
	if err != nil {
		return fmt.Errorf("locating nexgen: %w", err)
	}

	args := append([]string{options.language}, options.inputs...)
	for _, path := range options.descriptors {
		args = append(args, "--descriptors", path)
	}
	if options.supportPackage != "" {
		args = append(args, "--support-package", options.supportPackage)
	}
	if options.output != "" {
		args = append(args, "--output", options.output)
	}
	if options.systemNexus {
		args = append(args, "--system-nexus")
	}
	args = append(args, "--targets", "workflow")
	if options.format {
		args = append(args, "--format")
	}

	cmd := exec.Command(nexGenExe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nexgen: %w", err)
	}
	return nil
}

// checkEqual checks if the two byte arrays have the same contents, modulo LF/CRLF line endings.
func checkEqual(got, want []byte) bool {
	return bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")))
}
