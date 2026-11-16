package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
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
// console, network, and filesystem.
type effects interface {
	today() time.Time
	randomSuffix() string
	readCharacter() (byte, error)
	page(text string) error
	out(format string, args ...any)
	err(format string, args ...any)
	runCommand(dir, name string, args ...string) (string, error)
	runInteractiveCommand(dir, name string, args ...string) error
	mkdirTemp(dir, pattern string) (string, error)
	readFile(path string) (string, error)
	writeFile(path, contents string) error
	// checkModulePublished checks proxy.golang.org to decide if modulePath@version is published
	checkModulePublished(modulePath, version string) (bool, error)
}

type realWorld struct {
	stdout io.Writer
	stderr io.Writer
	audit  io.Writer
}

var _ effects = realWorld{}

func (eff realWorld) today() time.Time {
	return time.Now()
}

func (realWorld) randomSuffix() string {
	return strings.ToLower(rand.Text()[:8])
}

func (eff realWorld) readCharacter() (byte, error) {
	cmd := exec.Command("bash", "-c", `IFS= read -rsn1 key || exit; printf '%s' "$key"`)
	cmd.Stdin = os.Stdin
	cmd.Stderr = eff.stderr
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return 0, io.EOF
		}
		return 0, fmt.Errorf("read a key with bash (Bash must be on PATH): %w", err)
	}
	if len(output) == 0 {
		return '\n', nil
	}
	switch output[0] {
	case 3:
		return 0, errors.New("input canceled")
	case 4:
		return 0, io.EOF
	default:
		return output[0], nil
	}
}

func (eff realWorld) page(text string) error {
	pager, err := exec.LookPath("less")
	if errors.Is(err, exec.ErrNotFound) {
		eff.out("%s", text)
		return nil
	}
	if err != nil {
		return fmt.Errorf("find less: %w", err)
	}
	if err := eff.logAudit("$ less\n\n"); err != nil {
		return err
	}
	cmd := exec.Command(pager)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = eff.stdout
	cmd.Stderr = eff.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("view with less: %w", err)
	}
	return nil
}

func (eff realWorld) out(format string, args ...any) {
	_, err := fmt.Fprintf(eff.stdout, format, args...)
	if err != nil {
		panic(fmt.Errorf("write stdout: %w", err))
	}
}

func (eff realWorld) err(format string, args ...any) {
	_, err := fmt.Fprintf(eff.stderr, format, args...)
	if err != nil {
		panic(fmt.Errorf("write stderr: %w", err))
	}
}

// errorWriter routes flag package diagnostics through effects.
type errorWriter struct {
	eff effects
}

func (w errorWriter) Write(p []byte) (int, error) {
	w.eff.err("%s", p)
	return len(p), nil
}

func (eff realWorld) runCommand(dir, name string, args ...string) (string, error) {
	command := formatCommand(name, args...)
	if err := eff.logAudit("# Running in %s:\n$ %s\n", dir, command); err != nil {
		return "", err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	auditErr := eff.logCommandOutput(stdout.String(), stderr.String())
	if auditErr != nil {
		auditErr = fmt.Errorf("command %s already attempted; check its outcome before retrying: %w", command, auditErr)
	}
	if err != nil {
		if commandStderr := strings.TrimSpace(stderr.String()); commandStderr != "" {
			err = fmt.Errorf("%w\n%s", err, commandStderr)
		}
		return "", errors.Join(err, auditErr)
	}
	return stdout.String(), auditErr
}

// runInteractiveCommand leaves output attached to the terminal so commands can
// use their own pagers. The audit log records the command, not its terminal output.
func (eff realWorld) runInteractiveCommand(dir, name string, args ...string) error {
	command := formatCommand(name, args...)
	if err := eff.logAudit("# Running in %s:\n$ %s\n\n", dir, command); err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = eff.stdout
	cmd.Stderr = eff.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", command, err)
	}
	return nil
}

func (eff realWorld) logAudit(format string, args ...any) error {
	_, err := fmt.Fprintf(eff.audit, format, args...)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

func (eff realWorld) logCommandOutput(stdout, stderr string) error {
	output := stdout
	if output != "" && stderr != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	output += stderr
	output = strings.TrimRight(output, "\r\n")
	if output != "" {
		output += "\n"
	}
	return eff.logAudit("%s\n", output)
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
	if err := eff.logAudit("HTTP GET %s\n", url); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("query %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, errors.Join(fmt.Errorf("query %s: %w", url, err), eff.logAudit("%s\n\n", err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if err := eff.logAudit("%s\n\n", resp.Status); err != nil {
		return false, err
	}

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
