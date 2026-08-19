// Command gen-system-nexus runs nexgen to generate the System Nexus API.
//
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage: go run ./internal/cmd/gen-system-nexus [--preserve-build-dir]
//
// A pinned nexgen release is automatically installed unless NEX_GEN_BIN is set.
package main

import (
	"errors"
	"flag"
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

type commandOptions struct {
	preserveBuildDir bool
}

func main() {
	options := commandOptions{}
	flag.BoolVar(&options.preserveBuildDir, "preserve-build-dir", false, "preserve the build directory, even if the command succeeds")
	flag.Parse()
	if err := run(options); err != nil {
		log.Fatalf("gen-system-nexus: %v", err)
	}
}

func run(options commandOptions) (retErr error) {
	// Locate the SDK root directory and create a temporary build directory

	sdk, err := sdkRoot()
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "gen-system-nexus-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() {
		if options.preserveBuildDir || retErr != nil {
			log.Printf("Preserved build directory: %s", tmp)
			return
		}
		err := os.RemoveAll(tmp)
		if err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("removing build directory %s: %w", tmp, err))
			return
		}
		log.Printf("Removed build directory")
	}()

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
	log.Printf("Using nexgen executable: %s", nexGenExe)

	descriptorsFile, err := genProtoDescriptors(descriptorsBuildDir)
	if err != nil {
		return err
	}
	log.Printf("Generated proto descriptors: %s", descriptorsFile)

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
	log.Printf("Nexgen generated file: %s", serviceFile)

	// Run gofmt on the result and copy it to the destination.

	outputFile, err := formatAndCopyFile(dstPkgDir, serviceFile)
	if err != nil {
		return err
	}
	log.Printf("Wrote %s", outputFile)

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

// genProtoDescriptors generates a proto descriptor set for the workflowservice API.
// Equivalent to running protoc with --include_imports. Returns the generated file path.
func genProtoDescriptors(buildDir string) (string, error) {
	if err := os.Mkdir(buildDir, 0o700); err != nil {
		return "", fmt.Errorf("creating descriptor build directory: %w", err)
	}
	descriptorsFile := filepath.Join(buildDir, "descriptors.bin")
	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]struct{})
	addProtoDescriptor(set, seen, workflowservice.File_temporal_api_workflowservice_v1_request_response_proto)

	contents, err := proto.Marshal(set)
	if err != nil {
		return "", fmt.Errorf("marshaling Temporal API descriptors: %w", err)
	}
	if err := os.WriteFile(descriptorsFile, contents, 0o600); err != nil {
		return "", fmt.Errorf("writing Temporal API descriptors %s: %w", descriptorsFile, err)
	}
	return descriptorsFile, nil
}

// addProtoDescriptor recursively adds a file descriptor and its imports to the given set.
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

// formatAndCopyFile formats a Go source file, copies it into dstDir, and returns its output path.
func formatAndCopyFile(dstDir, inputFile string) (string, error) {
	contents, err := os.ReadFile(inputFile)
	if err != nil {
		return "", fmt.Errorf("reading generated Go file %s: %w", inputFile, err)
	}
	formatted, err := format.Source(contents)
	if err != nil {
		return "", fmt.Errorf("formatting generated Go file %s: %w", inputFile, err)
	}
	outputFile := filepath.Join(dstDir, filepath.Base(inputFile))
	if err := os.WriteFile(outputFile, formatted, 0o644); err != nil {
		return "", fmt.Errorf("writing generated Go file %s: %w", outputFile, err)
	}
	return outputFile, nil
}

// sdkRoot returns the root of the Go SDK source tree
func sdkRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not locate gen-system-nexus source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}
