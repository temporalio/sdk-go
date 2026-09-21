package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// goModuleProxy answers whether a module version was ever published.
const goModuleProxy = "https://proxy.golang.org"

// effects is a dependency injection object for operations that touch the
// network and filesystem.
type effects interface {
	runCommand(dir, name string, args ...string) (string, error)
	mkdirTemp(dir, pattern string) (string, error)
	readFile(path string) (string, error)
	writeFile(path, contents string) error
	// checkModulePublished checks proxy.golang.org to decide if modulePath@version is published
	checkModulePublished(modulePath, version string) (bool, error)
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

func (eff realWorld) checkModulePublished(modulePath, version string) (bool, error) {
	url := goModuleProxy + "/" + escapeModulePath(modulePath) + "/@v/" + version + ".info"
	printDetail(eff.out, "HTTP GET: %s...", url)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("query %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("query %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusGone:
		return false, nil
	default:
		return false, fmt.Errorf("query %s: unexpected response %s", url, resp.Status)
	}
}

// escapeModulePath encodes a module path for the module proxy, which requires
// every uppercase letter to be replaced by an exclamation mark and its lowercase
// form. See https://go.dev/ref/mod#goproxy-protocol.
func escapeModulePath(modulePath string) string {
	if !strings.ContainsFunc(modulePath, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		return modulePath
	}
	var escaped strings.Builder
	for _, r := range modulePath {
		if r >= 'A' && r <= 'Z' {
			escaped.WriteByte('!')
			r += 'a' - 'A'
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
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
