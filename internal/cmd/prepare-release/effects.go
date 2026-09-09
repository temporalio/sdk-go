package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// effects is a dependency injection object for operations that touch the
// network and filesystem.
type effects interface {
	runCommand(dir, name string, args ...string) (string, error)
	mkdirTemp(dir, pattern string) (string, error)
	readFile(path string) (string, error)
	writeFile(path, contents string) error
}

type realWorld struct {
	out io.Writer
}

var _ effects = realWorld{}

func (eff realWorld) runCommand(dir, name string, args ...string) (string, error) {
	printDetail(eff.out, "$ %s", formatCommand(name, args...))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
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

func (realWorld) mkdirTemp(dir, pattern string) (string, error) {
	return os.MkdirTemp(dir, pattern)
}

func (realWorld) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func (realWorld) writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}

// formatCommand renders a command with quoting suitable for logs.
func formatCommand(name string, args ...string) string {
	parts := []string{name}
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			arg = strconv.Quote(arg)
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}
