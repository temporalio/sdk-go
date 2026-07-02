// Command nexussystemgen runs nex-gen to generate the System Nexus API.
//
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage:
//
//	NEX_GEN_BIN=/path/to/nex-gen \
//	  go run ./internal/cmd/nexussystemgen \
//	  -descriptors /path/to/descriptor_set.pb
//
// nex-gen is located via the NEX_GEN_BIN environment variable, falling back to
// a `nex-gen` binary on PATH.
package main

import (
	"flag"
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
	descriptors := flag.String("descriptors", "", "path to the proto descriptor set (FileDescriptorSet)")
	flag.Parse()
	if *descriptors == "" {
		return fmt.Errorf("-descriptors is required")
	}

	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	witDir := filepath.Join(repoRoot, "internal", "nexussystem", "wit")
	witMain := filepath.Join(witDir, "workflow-service.wit")
	witDeps := filepath.Join(witDir, "deps")
	outPkgDir := filepath.Join(repoRoot, "workflow")

	tmpDir, err := os.MkdirTemp("", "nexussystemgen-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	nexGen := os.Getenv("NEX_GEN_BIN")
	if nexGen == "" {
		nexGen = "nex-gen"
	}

	cmd := exec.Command(nexGen, "generate",
		"--lang", "go",
		"--input", witMain,
		"--input", witDeps,
		"--descriptors", *descriptors,
		"--output", tmpDir,
		"--go-package", "go.temporal.io/sdk/workflow",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nex-gen: %w", err)
	}

	for srcName, dstName := range map[string]string{
		"api.go":             "system_nexus_gen.go",
		"model_overrides.go": "system_nexus_model_overrides_gen.go",
	} {
		src := filepath.Join(tmpDir, srcName)
		dst := filepath.Join(outPkgDir, dstName)
		if err := copyFile(dst, src); err != nil {
			return err
		}
		if err := gofmt(dst); err != nil {
			return err
		}
	}
	return nil
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
	if err == nil {
		abs, err := filepath.Abs(".")
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return "", fmt.Errorf("nexussystemgen must be run from the repository root (where go.mod lives)")
}
