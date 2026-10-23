// Command gen-system-nexus runs nexgen to generate the System Nexus API.
//
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage: go run ./internal/cmd/gen-system-nexus [--nexgen-rev <commit>] [--preserve-build-dir]
//
// A pinned nexgen Git revision is automatically installed with Cargo.
package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
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

const (
	nexGenRepository = "https://github.com/temporalio/nexgen"
	nexGenRevision   = "c03f1df573d737ebe63bb196d2989b6f839f83a4"
	witFilePath      = "workflowservice/v1/workflow-service.wit"
	witDepsPath      = "deps"
	witOverridesPath = "internal/cmd/gen-system-nexus/wit-overrides"
	dstPkgPath       = "workflow"
	supportPackage   = "go.temporal.io/sdk/internal/common/systemnexus"
)

func main() {
	sdk, err := sdkRoot()
	if err != nil {
		log.Fatalf("gen-system-nexus: %v", err)
	}
	options, err := parseOptions(os.Args[1:], sdk)
	if err != nil {
		log.Fatalf("gen-system-nexus: %v", err)
	}
	if err = run(options); err != nil {
		log.Fatalf("gen-system-nexus: %v", err)
	}
}

type runOptions struct {
	preserveBuildDir bool
	nexGenRevision   string
	witDir           string
	witOverridesDir  string
	dstPkgDir        string
}

func parseOptions(args []string, sdk string) (runOptions, error) {
	var options runOptions
	var err error

	flags := flag.NewFlagSet("gen-system-nexus", flag.ContinueOnError)
	flags.BoolVar(&options.preserveBuildDir, "preserve-build-dir", false, "preserve the build directory, even if the command succeeds")
	flags.StringVar(&options.nexGenRevision, "nexgen-rev", nexGenRevision, "full Git commit SHA of nexgen to install")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if revision, err := hex.DecodeString(options.nexGenRevision); err != nil || len(revision) != 20 {
		return options, fmt.Errorf("-nexgen-rev must be a full 40-character Git commit SHA, got %q", options.nexGenRevision)
	}
	apiDir, err := apiModuleDir(sdk)
	if err != nil {
		return options, err
	}
	options.witDir, err = resolveExistingPath(apiDir, "systemnexus")
	if err != nil {
		return options, err
	}
	options.witOverridesDir = filepath.Join(sdk, filepath.FromSlash(witOverridesPath))
	options.dstPkgDir, err = resolveExistingPath(sdk, dstPkgPath)
	if err != nil {
		return options, err
	}

	return options, nil
}

func apiModuleDir(sdk string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "go.temporal.io/api")
	cmd.Dir = sdk
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("locating go.temporal.io/api with go list: %w: %s", err, strings.TrimSpace(string(output)))
	}
	dir := strings.TrimSpace(string(output))
	if dir == "" {
		return "", fmt.Errorf("go list returned no directory for go.temporal.io/api")
	}
	return dir, nil
}

func resolveExistingPath(root, relativePath string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("resolving path %s: %w", path, err)
	}
	return path, nil
}

func run(options runOptions) (retErr error) {
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

	nexGenBuildDir := filepath.Join(tmp, "nexgen")
	witBuildDir := filepath.Join(tmp, "wit")
	descriptorsBuildDir := filepath.Join(tmp, "descriptors")
	serviceBuildDir := filepath.Join(tmp, "workflow")

	if err := stageWIT(options.witDir, options.witOverridesDir, witBuildDir); err != nil {
		return err
	}
	witFile, err := resolveExistingPath(witBuildDir, witFilePath)
	if err != nil {
		return err
	}
	witDepsDir, err := resolveExistingPath(witBuildDir, witDepsPath)
	if err != nil {
		return err
	}
	nexGenExe, err := getNexGenExecutable(nexGenBuildDir, options.nexGenRevision)
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

	outputFile, err := formatAndCopyFile(options.dstPkgDir, serviceFile)
	if err != nil {
		return err
	}
	log.Printf("Wrote %s", outputFile)

	return nil
}

func stageWIT(sourceDir, overridesDir, buildDir string) error {
	if err := os.CopyFS(buildDir, os.DirFS(sourceDir)); err != nil {
		return fmt.Errorf("copying API WIT from %s: %w", sourceDir, err)
	}
	if _, err := os.Stat(overridesDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("reading WIT overrides %s: %w", overridesDir, err)
	}
	return filepath.WalkDir(overridesDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("reading WIT override %s: %w", path, err)
		}
		relative, err := filepath.Rel(overridesDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(buildDir, relative)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".wit" {
			return fmt.Errorf("WIT override %s must be a regular .wit file", path)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading WIT override %s: %w", path, err)
		}
		if err := os.WriteFile(dst, contents, 0o600); err != nil {
			return fmt.Errorf("writing WIT override %s: %w", dst, err)
		}
		return nil
	})
}

// getNexGenExecutable installs the selected nexgen commit into the build directory.
func getNexGenExecutable(buildDir, revision string) (string, error) {
	nexGenPath := filepath.Join(buildDir, "bin", "nexgen")
	if runtime.GOOS == "windows" {
		nexGenPath += ".exe"
	}

	cmd := exec.Command(
		"cargo", "install", "--locked",
		"--features", "advanced",
		"--git", nexGenRepository,
		"--rev", revision,
		"--root", buildDir,
		"nexgen",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("installing nexgen revision %s with cargo: %w", revision, err)
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
		"--support-package", supportPackage,
		"--system-nexus",
		"--native-api",
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
