// Command nexussystemgen runs nex-gen to generate the System Nexus API.
//
// Must be invoked from the root of the Go SDK repo.
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage:
//
//	go run ./internal/cmd/nexussystemgen
//	go run ./internal/cmd/nexussystemgen --check
//
// A pinned nex-gen revision is automatically installed unless NEX_GEN_BIN is set.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
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
	nexGenRepo     = "https://github.com/temporalio/nex-gen"
	nexGenRevision = "696374363f1c57f66a6b5508c4b347b89aacaa0a"
)

type generatedFile struct {
	generated string
	checkedIn string
}

func main() {
	check := flag.Bool("check", false, "verify generated files are current without modifying them")
	flag.Parse()
	if err := run(*check); err != nil {
		log.Fatalf("nexussystemgen: %v", err)
	}
}

func run(check bool) error {
	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	witMain := filepath.Join(repoRoot, "internal", "nexussystem", "wit", "workflow-service.wit")
	witDeps := filepath.Join(filepath.Dir(witMain), "deps")
	outPkgDir := filepath.Join(repoRoot, "workflow")

	tmpDir, err := os.MkdirTemp("", "nexussystemgen-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	serviceDst := filepath.Join(outPkgDir, "workflowservice.go")
	serviceTmp := filepath.Join(tmpDir, "workflowservice.go")
	supportDst := filepath.Join(outPkgDir, "nexus_system_support.go")
	supportTmp := filepath.Join(tmpDir, "support.go")

	descriptors, err := buildDescriptorSet(tmpDir)
	if err != nil {
		return err
	}

	nexGen, err := nexGenBinary(repoRoot)
	if err != nil {
		return err
	}

	cmd := exec.Command(nexGen,
		"generate",
		"--lang", "go",
		"--input", witMain,
		"--input", witDeps,
		"--descriptors", descriptors,
		"--output", tmpDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nex-gen: %w", err)
	}

	files := []generatedFile{
		{generated: serviceTmp, checkedIn: serviceDst},
		{generated: supportTmp, checkedIn: supportDst},
	}
	for _, file := range files {
		if err := gofmt(file.generated); err != nil {
			return err
		}
	}
	if check {
		return checkGeneratedFiles(repoRoot, files)
	}
	if err := copyFile(serviceDst, serviceTmp); err != nil {
		return err
	}
	if err := copyFile(supportDst, supportTmp); err != nil {
		return err
	}
	return nil
}

func checkGeneratedFiles(repoRoot string, files []generatedFile) error {
	var stale []string
	for _, file := range files {
		generated, err := os.ReadFile(file.generated)
		if err != nil {
			return fmt.Errorf("reading generated file %s: %w", file.generated, err)
		}
		checkedIn, err := os.ReadFile(file.checkedIn)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("reading checked-in file %s: %w", file.checkedIn, err)
		}
		if err != nil || !bytes.Equal(generated, checkedIn) {
			path, relErr := filepath.Rel(repoRoot, file.checkedIn)
			if relErr != nil {
				path = file.checkedIn
			}
			stale = append(stale, filepath.ToSlash(path))
		}
	}
	if len(stale) != 0 {
		return fmt.Errorf(
			"generated files are stale: %s; run `go run ./internal/cmd/nexussystemgen`",
			strings.Join(stale, ", "),
		)
	}
	return nil
}

// buildDescriptorSet generates a proto descriptor set for the workflowservice API.
// Equivalent to running protoc with --include_imports.
// Returns the path to the generated descriptor set file.
func buildDescriptorSet(tmpDir string) (string, error) {
	path := filepath.Join(tmpDir, "descriptors.bin")
	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]struct{})
	addFileDescriptor(set, seen, workflowservice.File_temporal_api_workflowservice_v1_request_response_proto)

	contents, err := proto.Marshal(set)
	if err != nil {
		return "", fmt.Errorf("marshaling Temporal API descriptors: %w", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return "", fmt.Errorf("writing Temporal API descriptors %s: %w", path, err)
	}
	return path, nil
}

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

// nexGenBinary returns the path to the pinned nex-gen binary, installing it with cargo if necessary.
func nexGenBinary(repoRoot string) (string, error) {
	if nexGen := os.Getenv("NEX_GEN_BIN"); nexGen != "" {
		return nexGen, nil
	}
	toolRoot := filepath.Join(repoRoot, ".build", "tools", "nex-gen-"+nexGenRevision)
	binaryName := "nex-gen"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(toolRoot, "bin", binaryName)
	if _, err := os.Stat(binary); err == nil {
		return binary, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking nex-gen binary %s: %w", binary, err)
	}

	cmd := exec.Command(
		"cargo", "install", "--locked",
		"--git", nexGenRepo,
		"--rev", nexGenRevision,
		"--root", toolRoot,
		"nex-gen",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("installing nex-gen revision %s with cargo: %w", nexGenRevision, err)
	}
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("checking installed nex-gen binary %s: %w", binary, err)
	}
	return binary, nil
}

// copyFile copies a file from src to dst, creating dst if necessary.
func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copying %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", dst, err)
	}
	return nil
}

func gofmt(path string) error {
	cmd := exec.Command("gofmt", "-w", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gofmt %s: %w", path, err)
	}
	return nil
}

// repoRoot returns the absolute path to the root of the Go SDK repository,
// assuming this script lives at <repo>/internal/cmd/nexussystemgen/main.go.
func repoRoot() (string, error) {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("nexussystemgen must be run from the repository root")
	}
	modulePath := ""
	for _, line := range strings.Split(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			modulePath = fields[1]
			break
		}
	}
	if modulePath != "go.temporal.io/sdk" {
		return "", fmt.Errorf("nexussystemgen must be run from the go.temporal.io/sdk repository root")
	}
	abs, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	return abs, nil
}
