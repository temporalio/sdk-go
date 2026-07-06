// Command nexussystemgen runs nex-gen to generate the System Nexus API.
//
// Must be invoked from the root of the Go SDK repo.
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage:
//
//	go run ./internal/cmd/nexussystemgen
//
// Requires buf on PATH to build Temporal API descriptors.
// nex-gen is automatically installed unless NEX_GEN_BIN is set.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	overrideDst := filepath.Join(outPkgDir, "nexus_system_model_overrides.go")

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

	overrideSupport := filepath.Join(tmpDir, "model_overrides.go")
	if err := copyFile(overrideSupport, overrideDst); err != nil {
		return err
	}

	args := []string{
		"generate",
		"--lang", "go",
		"--input", witMain,
		"--support-file", overrideSupport,
	}
	if info, err := os.Stat(witDeps); err == nil && info.IsDir() {
		args = append(args, "--input", witDeps)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", witDeps, err)
	}
	args = append(args,
		"--descriptors", descriptors,
		"--output", tmpDir,
	)

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
	if err := copyFile(overrideDst, filepath.Join(tmpDir, "model_overrides.go")); err != nil {
		return err
	}
	if err := gofmt(overrideDst); err != nil {
		return err
	}
	return nil
}

func buildDescriptorSet(tmpDir string) (string, error) {
	if _, err := exec.LookPath("buf"); err != nil {
		return "", fmt.Errorf("buf is required to build Temporal API descriptors; install it with `go install github.com/bufbuild/buf/cmd/buf@v1.27.0`: %w", err)
	}

	path := filepath.Join(tmpDir, "descriptor_set.pb")
	cmd := exec.Command("buf", "build", "buf.build/temporalio/api", "--as-file-descriptor-set", "-o", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building Temporal API descriptors with buf failed; install or update buf with `go install github.com/bufbuild/buf/cmd/buf@v1.27.0`: %w", err)
	}
	return path, nil
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
	_, err := os.Stat("go.mod")
	if err != nil {
		return "", fmt.Errorf("nexussystemgen must be run from the repository root")
	}
	abs, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	return abs, nil
}
