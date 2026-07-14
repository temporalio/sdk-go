// Command nexussystemgen runs nex-gen to generate the System Nexus API.
//
// Must be invoked from the root of the Go SDK repo.
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage:
//
//	go run ./internal/cmd/nexussystemgen
//
// nex-gen is automatically installed unless NEX_GEN_BIN is set.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	workflowservice "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("nexussystemgen: %v", err)
	}
}

func run() error {
	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	witMain := filepath.Join(repoRoot, "internal", "nexussystem", "wit", "workflow-service.wit")
	if info, err := os.Stat(witMain); err != nil {
		return fmt.Errorf("stat %s: %w", witMain, err)
	} else if info.IsDir() {
		return fmt.Errorf("WIT input must be a file, got directory %s", witMain)
	}
	witDeps := filepath.Join(filepath.Dir(witMain), "deps")
	outPkgDir := filepath.Join(repoRoot, "workflow")

	tmpDir, err := os.MkdirTemp("", "nexussystemgen-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	descriptors, err := buildDescriptorSet(tmpDir)
	if err != nil {
		return err
	}

	nexGen, err := nexGenBinary()
	if err != nil {
		return err
	}

	args, err := nexGenArgs(witMain, witDeps, descriptors, tmpDir)
	if err != nil {
		return err
	}

	cmd := exec.Command(nexGen, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nex-gen: %w", err)
	}

	serviceDst := filepath.Join(outPkgDir, "workflowservice.go")
	if err := copyFile(serviceDst, filepath.Join(tmpDir, "workflowservice.go")); err != nil {
		return err
	}
	if err := gofmt(serviceDst); err != nil {
		return err
	}
	return nil
}

func nexGenArgs(witMain, witDeps, descriptors, output string) ([]string, error) {
	args := []string{
		"generate",
		"--lang", "go",
		"--input", witMain,
	}
	if info, err := os.Stat(witDeps); err == nil && info.IsDir() {
		args = append(args, "--input", witDeps)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat %s: %w", witDeps, err)
	}
	return append(args,
		"--descriptors", descriptors,
		"--output", output,
	), nil
}

func buildDescriptorSet(tmpDir string) (string, error) {
	path := filepath.Join(tmpDir, "descriptor_set.pb")
	// Generated API Go types retain their source descriptors, so no API checkout is needed.
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

func nexGenBinary() (string, error) {
	if nexGen := os.Getenv("NEX_GEN_BIN"); nexGen != "" {
		return nexGen, nil
	}
	if path, err := exec.LookPath("nex-gen"); err == nil {
		return path, nil
	}

	cmd := exec.Command("cargo", "install", "--locked", "nex-gen", "--force")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("installing nex-gen with cargo: %w", err)
	}
	if path, err := exec.LookPath("nex-gen"); err == nil {
		return path, nil
	}
	return "nex-gen", nil
}

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

func repoRoot() (string, error) {
	// This file lives at <repo>/internal/cmd/nexussystemgen/main.go.
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
