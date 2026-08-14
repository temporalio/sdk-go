package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	root, err := (RealWorld{}).repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != "temporal-sdk-go" {
		t.Fatalf("unexpected repository root: %q", root)
	}
}

func TestRunCommand(t *testing.T) {
	root, err := (RealWorld{}).repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	output, err := (RealWorld{}).runCommand(root, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != root {
		t.Fatalf("unexpected command output: %q", output)
	}
}

// TEST HARNESS

type command struct {
	root string
	name string
	args []string
}

func (cmd command) String() string {
	return formatCommand(cmd.name, cmd.args...)
}

type mockEffects struct {
	commandHandler func(command) (string, error)
	commands       strings.Builder
	output         strings.Builder
	repoRootPath   string
	tempDir        string
	files          map[string]string
}

func newMockEffects(handler func(command) (string, error)) *mockEffects {
	return &mockEffects{
		commandHandler: handler,
		repoRootPath:   "/repo",
		tempDir:        "/tmp/prepare-go-release-123456",
		files:          make(map[string]string),
	}
}

func (eff *mockEffects) printf(format string, args ...any) {
	fmt.Fprintf(&eff.output, format, args...)
}

func (eff *mockEffects) repoRoot() (string, error) {
	return eff.repoRootPath, nil
}

func (eff *mockEffects) runCommand(root, name string, args ...string) (string, error) {
	cmd := command{root: root, name: name, args: append([]string(nil), args...)}
	fmt.Fprintf(&eff.commands, "%s: %s\n", root, cmd.String())
	if eff.commandHandler == nil {
		return "", nil
	}
	return eff.commandHandler(cmd)
}

func (eff *mockEffects) mkdirTemp(string, string) (string, error) {
	return eff.tempDir, nil
}

func (eff *mockEffects) readFile(path string) (string, error) {
	contents, ok := eff.files[path]
	if !ok {
		return "", os.ErrNotExist
	}
	return contents, nil
}

func (eff *mockEffects) writeFile(path, contents string) error {
	eff.files[path] = contents
	return nil
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
	return strings.Join(lines, "\n")
}

// testEqual compares two strings, ignoring surrounding whitespace and common indentation.
func testEqual(t *testing.T, got, want string) {
	t.Helper()
	if got, want = stripIndentation(got), stripIndentation(want); got != want {
		t.Fatalf("unexpected text:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
