package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Effects interface {
	repoRoot() (string, error)
	runCommand(root, name string, args ...string) (string, error)
	readFile(path string) (string, error)
	updateFile(path string, update func(string) (string, error)) error
}

// REAL WORLD

type RealWorld struct{}

// repoRoot locates the SDK repository relative to this command's source file.
func (RealWorld) repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not locate prepare-release source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}

// runCommand executes a process, forwarding its output while also returning stdout.
func (RealWorld) runCommand(root, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &output)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w", formatCommand(name, args...), err)
	}
	return output.String(), nil
}

// readFile reads a file as text for the pure update and validation helpers.
func (RealWorld) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

// updateFile applies update and writes the resulting text back to path.
func (eff RealWorld) updateFile(path string, update func(string) (string, error)) error {
	data, err := eff.readFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	updated, err := update(data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// DRY RUN

type DryRun struct {
	Output  io.Writer
	TempDir string
}

// repoRoot uses the real repository layout without mutating the filesystem.
func (DryRun) repoRoot() (string, error) {
	return RealWorld{}.repoRoot()
}

func (eff DryRun) print(name string, args ...string) error {
	_, err := fmt.Fprintln(eff.Output, formatCommand(name, args...))
	return err
}

// runCommand prints the runCommand instead of executing it.
func (eff DryRun) runCommand(_ string, name string, args ...string) (string, error) {
	return "", eff.print(name, args...)
}

// readFile reads input needed to validate what a dry run would update.
func (DryRun) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

// updateFile writes the proposed contents to the dry-run directory.
func (eff DryRun) updateFile(path string, update func(string) (string, error)) error {
	data, err := eff.readFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	updated, err := update(data)
	if err != nil {
		return err
	}
	outputPath := filepath.Join(eff.TempDir, filepath.Base(path))
	if err := os.WriteFile(outputPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	_, err = fmt.Fprintf(eff.Output, "write %s\n", outputPath)
	return err
}

// MOCK WORLD

type MockWorld struct {
	Root          string
	Changelog     string
	Files         map[string]string
	Commands      []string
	CommandOutput map[string]string
	updatedFiles  map[string]bool
}

var _ Effects = RealWorld{}
var _ Effects = DryRun{}
var _ Effects = (*MockWorld)(nil)

// repoRoot returns the configured test root, defaulting to the fixture root.
func (eff *MockWorld) repoRoot() (string, error) {
	if eff.Root == "" {
		return "/repo", nil
	}
	return eff.Root, nil
}

// runCommand records commands and simulates git status from files updated in memory.
func (eff *MockWorld) runCommand(root, name string, args ...string) (string, error) {
	command := formatCommand(name, args...)
	eff.Commands = append(eff.Commands, command)
	if name != "git" || len(args) != 2 || args[0] != "status" || args[1] != "--porcelain" {
		return eff.CommandOutput[command], nil
	}
	var lines []string
	for path := range eff.updatedFiles {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		lines = append(lines, " M "+relative)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

// readFile reads mock changelog and repository files from memory.
func (eff *MockWorld) readFile(path string) (string, error) {
	if filepath.Base(path) == "CHANGELOG.md" {
		return eff.Changelog, nil
	}
	text, ok := eff.Files[path]
	if !ok {
		return "", fmt.Errorf("mock file not found: %s", path)
	}
	return text, nil
}

// updateFile applies updates in memory and tracks their paths for mock git status.
func (eff *MockWorld) updateFile(path string, update func(string) (string, error)) error {
	data, err := eff.readFile(path)
	if err != nil {
		return err
	}
	updated, err := update(data)
	if err != nil {
		return err
	}
	if filepath.Base(path) == "CHANGELOG.md" {
		eff.Changelog = updated
	} else {
		eff.Files[path] = updated
	}
	if eff.updatedFiles == nil {
		eff.updatedFiles = make(map[string]bool)
	}
	eff.updatedFiles[path] = true
	return nil
}
