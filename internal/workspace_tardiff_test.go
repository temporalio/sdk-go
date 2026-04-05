package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTarDiffFS_CreateAndMountPath(t *testing.T) {
	base := t.TempDir()
	fs := NewTarDiffFS(base)

	path, err := fs.Create("ws-1")
	require.NoError(t, err)
	assert.DirExists(t, path)
	assert.Equal(t, path, fs.MountPath("ws-1"))
}

func TestTarDiffFS_SnapshotAndRollback(t *testing.T) {
	base := t.TempDir()
	fs := NewTarDiffFS(base)

	path, err := fs.Create("ws-1")
	require.NoError(t, err)

	// Write a file.
	require.NoError(t, os.WriteFile(filepath.Join(path, "hello.txt"), []byte("hello"), 0o644))

	// Snapshot.
	require.NoError(t, fs.Snapshot("ws-1", "snap1"))

	// Modify file.
	require.NoError(t, os.WriteFile(filepath.Join(path, "hello.txt"), []byte("modified"), 0o644))

	// Rollback.
	require.NoError(t, fs.Rollback("ws-1", "snap1"))

	// Verify original content restored.
	data, err := os.ReadFile(filepath.Join(path, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestTarDiffFS_IncrementalDiffAndApply(t *testing.T) {
	base := t.TempDir()
	fs := NewTarDiffFS(base)

	path, err := fs.Create("ws-1")
	require.NoError(t, err)

	// Create initial state.
	require.NoError(t, os.WriteFile(filepath.Join(path, "file1.txt"), []byte("original"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v0"))

	// Make changes.
	require.NoError(t, os.WriteFile(filepath.Join(path, "file1.txt"), []byte("changed"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(path, "file2.txt"), []byte("new file"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v1"))

	// Generate diff.
	diffReader, diffSize, err := fs.IncrementalDiff("ws-1", "v0", "v1")
	require.NoError(t, err)
	assert.Greater(t, diffSize, int64(0))

	// Apply diff to a fresh workspace.
	path2, err := fs.Create("ws-2")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path2, "file1.txt"), []byte("original"), 0o644))

	require.NoError(t, fs.ApplyDiff("ws-2", diffReader))

	// Verify applied state.
	data, err := os.ReadFile(filepath.Join(path2, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "changed", string(data))

	data, err = os.ReadFile(filepath.Join(path2, "file2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new file", string(data))
}

func TestTarDiffFS_EmptyDiff(t *testing.T) {
	base := t.TempDir()
	fs := NewTarDiffFS(base)

	path, err := fs.Create("ws-1")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(path, "file.txt"), []byte("same"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v0"))

	// No changes.
	require.NoError(t, fs.Snapshot("ws-1", "v1"))

	diffReader, diffSize, err := fs.IncrementalDiff("ws-1", "v0", "v1")
	require.NoError(t, err)
	// No changes — reader is nil and size is 0.
	assert.Nil(t, diffReader)
	assert.Equal(t, int64(0), diffSize)
}

func TestTarDiffFS_Destroy(t *testing.T) {
	base := t.TempDir()
	fs := NewTarDiffFS(base)

	_, err := fs.Create("ws-1")
	require.NoError(t, err)
	require.NoError(t, fs.Snapshot("ws-1", "snap"))

	require.NoError(t, fs.Destroy("ws-1"))
	assert.NoDirExists(t, fs.MountPath("ws-1"))
}

func TestTarDiffFS_Subdirectories(t *testing.T) {
	base := t.TempDir()
	fs := NewTarDiffFS(base)

	path, err := fs.Create("ws-1")
	require.NoError(t, err)

	// Create nested structure.
	require.NoError(t, os.MkdirAll(filepath.Join(path, "src", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "src", "main.go"), []byte("package main"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v0"))

	// Add nested file.
	require.NoError(t, os.WriteFile(filepath.Join(path, "src", "pkg", "lib.go"), []byte("package pkg"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v1"))

	diffReader, _, err := fs.IncrementalDiff("ws-1", "v0", "v1")
	require.NoError(t, err)

	// Apply to fresh workspace with same base.
	path2, err := fs.Create("ws-2")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(path2, "src", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path2, "src", "main.go"), []byte("package main"), 0o644))

	require.NoError(t, fs.ApplyDiff("ws-2", diffReader))

	data, err := os.ReadFile(filepath.Join(path2, "src", "pkg", "lib.go"))
	require.NoError(t, err)
	assert.Equal(t, "package pkg", string(data))
}
