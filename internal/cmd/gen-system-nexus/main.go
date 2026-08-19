// Command gen-system-nexus runs nexgen to generate the System Nexus API.
//
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage: go run ./internal/cmd/gen-system-nexus
//
// A pinned nexgen release is automatically installed unless NEX_GEN_BIN is set.
package main

import (
	"fmt"
	"go/format"
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

const nexGenVersion = "0.2.1"

func main() {
	if err := run(); err != nil {
		log.Fatalf("gen-system-nexus: %v", err)
	}
}

func run() error {
	// Locate the SDK root directory and create a temporary build directory

	sdk, err := sdkRoot()
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "gen-system-nexus-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}

	witFile := filepath.Join(sdk, "internal", "nexussystem", "wit", "workflow-service.wit")
	witDepsDir := filepath.Join(sdk, "internal", "nexussystem", "wit", "deps")
	dstPkgDir := filepath.Join(sdk, "workflow")

	nexGenBuildDir := filepath.Join(tmp, "nexgen")
	descriptorsBuildDir := filepath.Join(tmp, "descriptors")
	serviceBuildDir := filepath.Join(tmp, "workflow")

	// Get the nex-gen exe, build proto descriptors, and run nex-gen to generate the code we want.

	nexGenExe, err := getNexGenExecutable(nexGenBuildDir)
	if err != nil {
		return err
	}

	descriptorsFile, err := genDescriptorSet(descriptorsBuildDir)
	if err != nil {
		return err
	}

	serviceFile, err := genService(genServiceOptions{
		nexGenExe:       nexGenExe,
		descriptorsFile: descriptorsFile,
		witFile:         witFile,
		witDepsDir:      witDepsDir,
		buildDir:        serviceBuildDir,
	})
	if err != nil {
		return err
	}

	// Run gofmt on the result, copy them to the destination, and clean up the build directory.

	err = formatAndMoveFiles(dstPkgDir, []string{serviceFile})
	if err != nil {
		return err
	}

	err = os.RemoveAll(tmp)
	if err != nil {
		return fmt.Errorf("removing build directory %s: %w", tmp, err)
	}

	return nil
}

// getNexGenExecutable returns the nexgen executable, installing a pinned version unless overridden.
func getNexGenExecutable(buildDir string) (string, error) {
	if override := os.Getenv("NEX_GEN_BIN"); override != "" {
		return override, nil
	}
	nexGenPath := filepath.Join(buildDir, "bin", "nexgen")
	if runtime.GOOS == "windows" {
		nexGenPath += ".exe"
	}

	cmd := exec.Command(
		"cargo", "install", "--locked",
		"--features", "advanced",
		"--version", "="+nexGenVersion,
		"--root", buildDir,
		"nexgen",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("installing nexgen version %s with cargo: %w", nexGenVersion, err)
	}
	if _, err := os.Stat(nexGenPath); err != nil {
		return "", fmt.Errorf("checking installed nexgen binary %s: %w", nexGenPath, err)
	}
	return nexGenPath, nil
}

// genDescriptorSet generates a proto descriptor set for the workflowservice API.
// Equivalent to running protoc with --include_imports. Returns the generated file path.
func genDescriptorSet(buildDir string) (string, error) {
	if err := os.Mkdir(buildDir, 0o700); err != nil {
		return "", fmt.Errorf("creating descriptor build directory: %w", err)
	}
	descriptorsFile := filepath.Join(buildDir, "descriptors.bin")
	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]struct{})
	addFileDescriptor(set, seen, workflowservice.File_temporal_api_workflowservice_v1_request_response_proto)

	contents, err := proto.Marshal(set)
	if err != nil {
		return "", fmt.Errorf("marshaling Temporal API descriptors: %w", err)
	}
	if err := os.WriteFile(descriptorsFile, contents, 0o600); err != nil {
		return "", fmt.Errorf("writing Temporal API descriptors %s: %w", descriptorsFile, err)
	}
	return descriptorsFile, nil
}

// addFileDescriptor recursively adds a file descriptor and its imports to the given set.
func addFileDescriptor(set *descriptorpb.FileDescriptorSet, seen map[string]struct{}, file protoreflect.FileDescriptor) {
	if _, ok := seen[file.Path()]; ok {
		return
	}
	seen[file.Path()] = struct{}{}
	imports := file.Imports()
	for i := range imports.Len() {
		addFileDescriptor(set, seen, imports.Get(i))
	}
	set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
}

type genServiceOptions struct {
	nexGenExe       string
	witFile         string
	witDepsDir      string
	descriptorsFile string
	buildDir        string
}

// genService runs nexgen on the given WIT file and returns the generated Go service file path.
func genService(options genServiceOptions) (string, error) {
	cmd := exec.Command(options.nexGenExe,
		"go",
		options.witFile,
		options.witDepsDir,
		"--descriptors", options.descriptorsFile,
		"--output", options.buildDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running nexgen: %w", err)
	}
	serviceName := strings.ReplaceAll(strings.TrimSuffix(filepath.Base(options.witFile), ".wit"), "-", "") + ".go"
	serviceFile := filepath.Join(options.buildDir, serviceName)
	if _, err := os.Stat(serviceFile); err != nil {
		return "", fmt.Errorf("checking generated service file %s: %w", serviceFile, err)
	}
	return serviceFile, nil
}

// formatAndMoveFiles formats Go source files and moves them into dstDir.
func formatAndMoveFiles(dstDir string, inputFiles []string) error {
	for _, inputFile := range inputFiles {
		contents, err := os.ReadFile(inputFile)
		if err != nil {
			return fmt.Errorf("reading generated Go file %s: %w", inputFile, err)
		}
		formatted, err := format.Source(contents)
		if err != nil {
			return fmt.Errorf("formatting generated Go file %s: %w", inputFile, err)
		}
		if err := os.WriteFile(inputFile, formatted, 0o644); err != nil {
			return fmt.Errorf("writing formatted Go file %s: %w", inputFile, err)
		}
		outputFile := filepath.Join(dstDir, filepath.Base(inputFile))
		// Rename only after formatting succeeds so a failed write cannot truncate the checked-in destination.
		if err := os.Rename(inputFile, outputFile); err != nil {
			return fmt.Errorf("moving formatted Go file from %s to %s: %w", inputFile, outputFile, err)
		}
	}
	return nil
}

// sdkRoot returns the root of the Go SDK source tree
func sdkRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not locate gen-system-nexus source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}
