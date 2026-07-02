// Command nexussystemgen runs nex-gen to generate the System Nexus API.
//
// The generated bindings are emitted directly into go.temporal.io/sdk/workflow.
//
// Usage:
//
//	NEX_GEN_BIN=/path/to/nex-gen \
//	  go run ./internal/cmd/nexussystemgen \
//	  -input internal/nexussystem/wit/workflow-service.wit \
//	  -descriptors /path/to/descriptor_set.pb
//
// nex-gen is located via the NEX_GEN_BIN environment variable, falling back to
// a `nex-gen` binary on PATH, then to `cargo install`.
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
	input := flag.String("input", "", "path to the WIT service file")
	descriptors := flag.String("descriptors", "", "path to the proto descriptor set (FileDescriptorSet)")
	flag.Parse()
	if *input == "" {
		return fmt.Errorf("-input is required")
	}
	if *descriptors == "" {
		return fmt.Errorf("-descriptors is required")
	}

	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	witMain, err := filepath.Abs(*input)
	if err != nil {
		return fmt.Errorf("resolving -input: %w", err)
	}
	if info, err := os.Stat(witMain); err != nil {
		return fmt.Errorf("stat %s: %w", witMain, err)
	} else if info.IsDir() {
		return fmt.Errorf("-input must be a WIT file, got directory %s", witMain)
	}
	witDeps := filepath.Join(filepath.Dir(witMain), "deps")
	supportFile := filepath.Join(repoRoot, "internal", "cmd", "nexussystemgen", "support", "model_overrides.go")
	outPkgDir := filepath.Join(repoRoot, "workflow")

	tmpDir, err := os.MkdirTemp("", "nexussystemgen-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	nexGen, err := nexGenBinary()
	if err != nil {
		return err
	}

	args := []string{
		"generate",
		"--lang", "go",
		"--input", witMain,
	}
	if info, err := os.Stat(witDeps); err == nil && info.IsDir() {
		args = append(args, "--input", witDeps)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", witDeps, err)
	}
	args = append(args,
		"--descriptors", *descriptors,
		"--output", tmpDir,
		"--go-package", "go.temporal.io/sdk/workflow",
		"--support-file", supportFile,
	)

	cmd := exec.Command(nexGen, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nex-gen: %w", err)
	}

	serviceFile, err := generatedServiceFile(tmpDir, "model_overrides.go")
	if err != nil {
		return err
	}
	for _, file := range []struct {
		srcName string
		dstName string
	}{
		{srcName: serviceFile, dstName: serviceFile},
		{srcName: "model_overrides.go", dstName: "system_nexus_model_overrides_gen.go"},
	} {
		src := filepath.Join(tmpDir, file.srcName)
		dst := filepath.Join(outPkgDir, file.dstName)
		if err := copyFile(dst, src); err != nil {
			return err
		}
		if err := gofmt(dst); err != nil {
			return err
		}
	}
	return nil
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

func generatedServiceFile(dir string, supportFileNames ...string) (string, error) {
	supportFiles := make(map[string]struct{}, len(supportFileNames))
	for _, name := range supportFileNames {
		supportFiles[name] = struct{}{}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading generated output dir %s: %w", dir, err)
	}
	var serviceFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".go" {
			continue
		}
		if _, ok := supportFiles[name]; ok {
			continue
		}
		serviceFiles = append(serviceFiles, name)
	}
	if len(serviceFiles) != 1 {
		return "", fmt.Errorf("expected one generated service file in %s, got %v", dir, serviceFiles)
	}
	return serviceFiles[0], nil
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
