package internal

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifest_TarOverlayLayerChunked(t *testing.T) {
	dir := t.TempDir()

	// Create files in layer dir.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "file2.txt"), []byte("world!"), 0o644))

	chunks, manifest, err := tarOverlayLayerChunked(dir, defaultChunkSize)
	require.NoError(t, err)
	assert.NotEmpty(t, chunks)

	// Check manifest structure.
	assert.Len(t, manifest.Files, 2)
	assert.Contains(t, manifest.Directories, "subdir")
	assert.Empty(t, manifest.Deleted)

	// Verify file entries have chunk IDs.
	for _, f := range manifest.Files {
		assert.NotEmpty(t, f.ChunkID, "file %s should have a chunk ID", f.Path)
		switch f.Path {
		case "file1.txt":
			assert.Equal(t, int64(5), f.Size)
		case filepath.Join("subdir", "file2.txt"):
			assert.Equal(t, int64(6), f.Size)
		default:
			t.Errorf("unexpected file in manifest: %s", f.Path)
		}
	}

	// Verify chunks are valid tar archives containing expected files.
	allFiles := make(map[string]string) // path → content
	for _, chunk := range chunks {
		tr := tar.NewReader(bytes.NewReader(chunk.Data))
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
				data, err := io.ReadAll(tr)
				require.NoError(t, err)
				allFiles[hdr.Name] = string(data)
			}
		}
	}
	assert.Equal(t, "hello", allFiles["file1.txt"])
	assert.Equal(t, "world!", allFiles[filepath.Join("subdir", "file2.txt")])
}

func TestManifest_TarOverlayLayerChunked_Whiteout(t *testing.T) {
	dir := t.TempDir()

	// Create a whiteout file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".wh.deleted.txt"), []byte{}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("kept"), 0o644))

	_, manifest, err := tarOverlayLayerChunked(dir, defaultChunkSize)
	require.NoError(t, err)

	assert.Len(t, manifest.Deleted, 1)
	assert.Equal(t, "deleted.txt", manifest.Deleted[0])
	assert.Len(t, manifest.Files, 1)
	assert.Equal(t, "kept.txt", manifest.Files[0].Path)
}

func TestManifest_TarOverlayLayerChunked_SmallDiff(t *testing.T) {
	dir := t.TempDir()

	// Single small file → should produce exactly one chunk.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tiny.txt"), []byte("x"), 0o644))

	chunks, manifest, err := tarOverlayLayerChunked(dir, defaultChunkSize)
	require.NoError(t, err)
	assert.Len(t, chunks, 1)
	assert.Len(t, manifest.Files, 1)
	assert.Equal(t, chunks[0].ID, manifest.Files[0].ChunkID)
}

func TestManifest_TarOverlayLayerChunked_LargeFile(t *testing.T) {
	dir := t.TempDir()

	// Create a file larger than chunkSize.
	chunkSize := int64(1024)
	bigData := bytes.Repeat([]byte("A"), int(chunkSize)+500)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.bin"), bigData, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hi"), 0o644))

	chunks, manifest, err := tarOverlayLayerChunked(dir, chunkSize)
	require.NoError(t, err)

	// The big file should get its own chunk.
	assert.GreaterOrEqual(t, len(chunks), 2)

	// Verify all files are in the manifest.
	assert.Len(t, manifest.Files, 2)

	// Verify the big file's chunk is different from the small file's.
	var bigChunkID, smallChunkID string
	for _, f := range manifest.Files {
		if f.Path == "big.bin" {
			bigChunkID = f.ChunkID
		}
		if f.Path == "small.txt" {
			smallChunkID = f.ChunkID
		}
	}
	assert.NotEmpty(t, bigChunkID)
	assert.NotEmpty(t, smallChunkID)
	assert.NotEqual(t, bigChunkID, smallChunkID)

	// Verify the big file's content survives round-trip.
	for _, chunk := range chunks {
		if chunk.ID == bigChunkID {
			tr := tar.NewReader(bytes.NewReader(chunk.Data))
			hdr, err := tr.Next()
			require.NoError(t, err)
			assert.Equal(t, "big.bin", hdr.Name)
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			assert.Equal(t, bigData, data)
		}
	}
}

func TestManifest_MergeManifests(t *testing.T) {
	m1 := &Manifest{
		Files: []ManifestFileEntry{
			{Path: "a.txt", Size: 10, ChunkID: "chunk-0000"},
			{Path: "b.txt", Size: 20, ChunkID: "chunk-0000"},
		},
		Directories: []string{"dir1"},
		Chunks: map[string]ManifestChunk{
			"chunk-0000": {Claim: map[string]string{"path": "/data/c0"}},
		},
	}
	m2 := &Manifest{
		Files: []ManifestFileEntry{
			{Path: "a.txt", Size: 15, ChunkID: "chunk-0001"}, // overrides m1
			{Path: "c.txt", Size: 30, ChunkID: "chunk-0001"},
		},
		Deleted: []string{"b.txt"}, // deletes from m1
		Chunks: map[string]ManifestChunk{
			"chunk-0001": {Claim: map[string]string{"path": "/data/c1"}},
		},
	}

	merged := MergeManifests([]*Manifest{m1, m2})

	// a.txt should be overridden to size 15.
	aEntry, ok := merged.manifestLookup("a.txt")
	assert.True(t, ok)
	assert.Equal(t, int64(15), aEntry.Size)
	assert.Equal(t, "chunk-0001", aEntry.ChunkID)

	// b.txt should be deleted.
	_, ok = merged.manifestLookup("b.txt")
	assert.False(t, ok)

	// c.txt should exist.
	cEntry, ok := merged.manifestLookup("c.txt")
	assert.True(t, ok)
	assert.Equal(t, int64(30), cEntry.Size)

	// dir1 should still exist.
	assert.True(t, merged.hasDir("dir1"))

	// Both chunk maps should be present.
	assert.Contains(t, merged.Chunks, "chunk-0000")
	assert.Contains(t, merged.Chunks, "chunk-0001")
}

func TestManifest_JSONRoundTrip(t *testing.T) {
	original := &Manifest{
		DriverName: "local-file-store",
		ChunkSize:  4 * 1024 * 1024,
		Chunks: map[string]ManifestChunk{
			"chunk-0000": {Claim: map[string]string{"path": "/data/c0"}},
		},
		Files: []ManifestFileEntry{
			{Path: "test.txt", Mode: 0o644, Size: 42, ChunkID: "chunk-0000"},
		},
		Directories: []string{"src"},
		Deleted:     []string{"old.txt"},
	}

	data, err := MarshalManifest(original)
	require.NoError(t, err)

	restored, err := UnmarshalManifest(data)
	require.NoError(t, err)

	assert.Equal(t, original.DriverName, restored.DriverName)
	assert.Equal(t, original.ChunkSize, restored.ChunkSize)
	assert.Equal(t, original.Chunks, restored.Chunks)
	assert.Equal(t, original.Files, restored.Files)
	assert.Equal(t, original.Directories, restored.Directories)
	assert.Equal(t, original.Deleted, restored.Deleted)
}

func TestManifest_ChildEntries(t *testing.T) {
	m := &Manifest{
		Files: []ManifestFileEntry{
			{Path: "a.txt"},
			{Path: "src/main.go"},
			{Path: "src/pkg/lib.go"},
		},
		Directories: []string{"src", "src/pkg"},
	}

	// Root children.
	children := m.childEntries("")
	assert.ElementsMatch(t, []string{"a.txt", "src"}, children)

	// src children.
	children = m.childEntries("src")
	assert.ElementsMatch(t, []string{"main.go", "pkg"}, children)
}

func TestManifest_ChunkExtraction(t *testing.T) {
	// Create a layer, generate chunks, then extract each chunk and verify
	// the files match the original content.
	dir := t.TempDir()
	content := "exact content here"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644))

	chunks, manifest, err := tarOverlayLayerChunked(dir, defaultChunkSize)
	require.NoError(t, err)
	require.Len(t, manifest.Files, 1)
	require.Len(t, chunks, 1)

	// Extract the chunk tar to a temp dir and verify file contents.
	extractDir := t.TempDir()
	err = extractTarToDir(extractDir, bytes.NewReader(chunks[0].Data))
	require.NoError(t, err)

	extracted, err := os.ReadFile(filepath.Join(extractDir, "test.txt"))
	require.NoError(t, err)
	assert.Equal(t, content, string(extracted))
}
