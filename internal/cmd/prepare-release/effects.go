package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Effects interface {
	// printf prints to stdout.
	printf(format string, args ...any)
	// repoRoot locates the SDK repository relative to this command's source file.
	repoRoot() (string, error)
	// runCommand executes a command with the given arguments and returns its stdout.
	runCommand(root, name string, args ...string) (string, error)
	// mkdirTemp creates a temporary directory and returns its path.
	mkdirTemp(dir, pattern string) (string, error)
	// readFile reads a file as text.
	readFile(path string) (string, error)
	// writeFile writes text to a file.
	writeFile(path, contents string) error
}

type RealWorld struct{}

var _ Effects = RealWorld{}

func (RealWorld) printf(format string, args ...any) {
	fmt.Printf(format, args...)
}

func (RealWorld) repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not locate prepare-release source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}

func (eff RealWorld) runCommand(root, name string, args ...string) (string, error) {
	eff.printf("> %s...\n", formatCommand(name, args...))
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if commandStderr := strings.TrimSpace(stderr.String()); commandStderr != "" {
			return "", fmt.Errorf("%w\n%s", err, commandStderr)
		}
		return "", err
	}
	return stdout.String(), nil
}

func (RealWorld) mkdirTemp(dir, pattern string) (string, error) {
	return os.MkdirTemp(dir, pattern)
}

func (RealWorld) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func (RealWorld) writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}
