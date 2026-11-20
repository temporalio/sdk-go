package pure

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testRegexp(t *testing.T, expression *regexp.Regexp, matches, rejects []string) {
	t.Helper()
	for _, input := range matches {
		if !expression.MatchString(input) {
			t.Errorf("expected %q to match %s", input, expression)
		}
	}
	for _, input := range rejects {
		if expression.MatchString(input) {
			t.Errorf("expected %q not to match %s", input, expression)
		}
	}
}

func stripIndentation(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
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
	if indent > 0 {
		for i, line := range lines {
			if len(line) >= indent {
				lines[i] = line[indent:]
			}
		}
	}
	return filepath.ToSlash(strings.Join(lines, "\n"))
}

func testEqual(t *testing.T, got, want string) {
	t.Helper()
	if got, want = stripIndentation(got), stripIndentation(want); got != want {
		t.Fatalf("unexpected text:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
