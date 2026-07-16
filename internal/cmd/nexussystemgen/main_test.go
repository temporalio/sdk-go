package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	write := func(path, contents string) string {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	generatedA := write("generated/a.go", "a")
	generatedB := write("generated/b.go", "b")
	checkedA := write("workflow/a.go", "a")
	checkedB := write("workflow/b.go", "b")
	files := []generatedFile{
		{generated: generatedA, checkedIn: checkedA},
		{generated: generatedB, checkedIn: checkedB},
	}

	if err := checkGeneratedFiles(root, files); err != nil {
		t.Fatalf("matching files reported stale: %v", err)
	}
	if err := os.WriteFile(checkedA, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(checkedB); err != nil {
		t.Fatal(err)
	}
	err := checkGeneratedFiles(root, files)
	if err == nil {
		t.Fatal("stale files were accepted")
	}
	for _, want := range []string{"workflow/a.go", "workflow/b.go", "go run ./internal/cmd/nexussystemgen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
