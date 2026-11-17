package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

const (
	mockTempDir  = "/tmp/prepare-go-release-123456"
	mockRepoRoot = "/repo"
)

// Relative fixture paths belong to the temporary worktree; absolute paths model other checkouts.
// Copy the fixtures so assertions can compare modified files against their original contents.
func newMockApp(a app, input map[string]string, handler func(cmd string) (string, error)) (*app, *mockEffects) {
	eff := &mockEffects{
		commandHandler: func(cmd string) (string, error) {
			if cmd == "git rev-parse HEAD" {
				return strings.TrimSpace(sdkRustRevision), nil
			}
			if handler != nil {
				return handler(cmd)
			}
			return "", nil
		},
		tempDir: mockTempDir,
		files:   make(map[string]string),
	}
	maps.Copy(eff.files, input)
	a.eff, a.repoRoot = eff, mockRepoRoot
	return &a, eff
}

func newMockVet(c vetApp, input map[string]string, handler func(cmd string) (string, error)) (*vetApp, *mockEffects) {
	a, eff := newMockApp(c.app, input, handler)
	c.app = *a
	return &c, eff
}

func newMockStage(c stageApp, input map[string]string, handler func(cmd string) (string, error)) (*stageApp, *mockEffects) {
	a, eff := newMockApp(c.app, input, handler)
	c.app = *a
	return &c, eff
}

func newMockTag(c tagApp, input map[string]string, handler func(cmd string) (string, error)) (*tagApp, *mockEffects) {
	a, eff := newMockApp(c.app, input, handler)
	c.app = *a
	return &c, eff
}

// mockEffects is a mock implementation of [effects] for testing.
type mockEffects struct {
	commandHandler func(cmd string) (string, error)
	audit          strings.Builder
	output         strings.Builder
	errors         strings.Builder
	tempDir        string
	files          map[string]string
	input          string
	// moduleLookupHandler answers checkModulePublished.
	// A nil handler treats every version as published.
	moduleLookupHandler func(modulePath, version string) (bool, error)
}

var _ effects = &mockEffects{}

func (eff *mockEffects) page(string) error {
	return nil
}

func (eff *mockEffects) runInteractiveCommand(string, string, ...string) error {
	return nil
}

func (eff *mockEffects) readCharacter() (byte, error) {
	if eff.input == "" {
		return 0, io.EOF
	}
	character := eff.input[0]
	eff.input = eff.input[1:]
	return character, nil
}

func (eff *mockEffects) today() time.Time {
	return time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
}

func (eff *mockEffects) randomSuffix() string {
	return "abc234de"
}

func (eff *mockEffects) out(format string, args ...any) {
	_, _ = fmt.Fprintf(&eff.output, format, args...)
}

func (eff *mockEffects) err(format string, args ...any) {
	_, _ = fmt.Fprintf(&eff.errors, format, args...)
}

func (eff *mockEffects) runCommand(root, name string, args ...string) (string, error) {
	// Normalize paths before quoting so command escapes are preserved on Windows.
	normalizedArgs := make([]string, len(args))
	for i, arg := range args {
		normalizedArgs[i] = filepath.ToSlash(arg)
	}
	cmd := formatCommand(name, normalizedArgs...)
	audit := realWorld{audit: &eff.audit}
	if err := audit.logAudit("# Running in %s:\n$ %s\n", root, cmd); err != nil {
		return "", err
	}
	var output string
	var err error
	if eff.commandHandler != nil {
		output, err = eff.commandHandler(cmd)
	}
	var stderr string
	if err != nil {
		stderr = err.Error()
	}
	if auditErr := audit.logCommandOutput(output, stderr); auditErr != nil {
		return "", auditErr
	}
	return output, err
}

func (eff *mockEffects) mkdirTemp(string, string) (string, error) {
	return eff.tempDir, nil
}

func (eff *mockEffects) readFile(path string) (string, error) {
	key := strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(eff.tempDir)+"/")
	contents, ok := eff.files[key]
	if !ok {
		return "", fmt.Errorf("read %s: %w", path, os.ErrNotExist)
	}
	return contents, nil
}

func (eff *mockEffects) writeFile(path, contents string) error {
	key := strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(eff.tempDir)+"/")
	eff.files[key] = contents
	return nil
}

func (eff *mockEffects) checkModulePublished(modulePath, version string) (bool, error) {
	if eff.moduleLookupHandler == nil {
		return true, nil
	}
	return eff.moduleLookupHandler(modulePath, version)
}

// contribEnvconfig is the module used by the contrib tests.
func contribEnvconfig(t *testing.T) pure.Module {
	t.Helper()
	m, err := pure.ContribModule("contrib/envconfig")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// stripIndentation removes surrounding blank lines and indentation shared by every nonblank line.
func stripIndentation(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	// Strip blank lines from the top and bottom of the string
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Set indent to the smallest number of \t characters preceding any nonblank line.
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lineIndent := len(line) - len(strings.TrimLeft(line, "\t"))
		if indent == -1 || lineIndent < indent {
			indent = lineIndent
		}
	}
	// Remove indentation
	if indent > 0 {
		for i, line := range lines {
			if len(line) >= indent {
				lines[i] = line[indent:]
			}
		}
	}
	return filepath.ToSlash(strings.Join(lines, "\n"))
}

// testEqual compares two strings, ignoring surrounding whitespace and common indentation.
func testEqual(t *testing.T, got, want string) {
	t.Helper()
	if got, want = stripIndentation(got), stripIndentation(want); got != want {
		t.Fatalf("unexpected text:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func testEqualMaps(t *testing.T, got, want map[string]string) {
	t.Helper()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected files (-want +got):\n%s", diff)
	}
}

func TestMockEffectsPreservesCommandEscapes(t *testing.T) {
	want := `git commit -m "Prepare release 2026-08-04\n\ncontrib/envconfig=0.2.0" -- contrib/envconfig/CHANGELOG.md`
	called := false
	_, eff := newMockApp(app{}, nil, func(cmd string) (string, error) {
		called = true
		if cmd != want {
			t.Fatalf("unexpected command: got %q, want %q", cmd, want)
		}
		return "", nil
	})
	_, err := eff.runCommand(mockRepoRoot, "git", "commit", "-m",
		"Prepare release 2026-08-04\n\ncontrib/envconfig=0.2.0", "--",
		filepath.Join("contrib", "envconfig", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("command handler was not called")
	}
}
