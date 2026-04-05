package internal

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ChunkLoader retrieves a chunk from remote storage and walks its contents.
// The callback is invoked for each file in the chunk tar with its relative
// path, file mode, and a reader for the file content. Directories are created
// automatically; the callback is only called for regular files.
type ChunkLoader interface {
	WalkChunk(ctx context.Context, chunkID string, fn func(path string, mode os.FileMode, r io.Reader) error) error
}

// ManifestChunk records the storage claim for a single tar chunk.
type ManifestChunk struct {
	Claim map[string]string `json:"claim"`
}

// Manifest describes the contents of a diff split into tar chunks.
// Each file entry points to the chunk that contains it via ChunkID.
type Manifest struct {
	// DriverName is the name of the storage driver that holds the chunks.
	DriverName string `json:"driver_name"`
	// ChunkSize is the target chunk size in bytes used during generation.
	ChunkSize int64 `json:"chunk_size"`
	// Chunks maps chunk IDs to their storage claims.
	Chunks map[string]ManifestChunk `json:"chunks"`
	// Files lists every regular file in the diff with its chunk assignment.
	Files []ManifestFileEntry `json:"files"`
	// Directories lists every directory path in the diff.
	Directories []string `json:"directories"`
	// Deleted lists paths that were removed (whiteout entries).
	Deleted []string `json:"deleted"`

	// Indexes for O(1) lookups, built by buildIndex(). Not serialized.
	fileIndex    map[string]int  // path → index in Files
	dirIndex     map[string]bool // directory paths
	deletedIndex map[string]bool // deleted paths
}

// ManifestFileEntry records metadata and the chunk assignment of a single file.
type ManifestFileEntry struct {
	Path    string        `json:"path"`
	Mode    iofs.FileMode `json:"mode"`
	Size    int64         `json:"size"`
	ChunkID string        `json:"chunk_id"`
}

// ChunkBlob is an in-memory tar chunk ready for upload.
type ChunkBlob struct {
	ID   string
	Data []byte
}

const defaultChunkSize = 4 * 1024 * 1024 // 4MB

// tarOverlayLayerChunked packs the contents of layerDir into tar chunks of at
// most chunkSize bytes. Files larger than chunkSize get their own chunk.
// Returns the chunk blobs (with sequential IDs) and a manifest.
func tarOverlayLayerChunked(layerDir string, chunkSize int64) ([]*ChunkBlob, *Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	manifest := &Manifest{
		ChunkSize: chunkSize,
		Chunks:    make(map[string]ManifestChunk),
	}

	type pendingFile struct {
		rel  string
		mode iofs.FileMode
		size int64
		path string // on-disk path
	}
	type pendingMeta struct {
		header *tar.Header
		isDel  bool
		delRel string
		isDir  bool
		dirRel string
	}

	var files []pendingFile
	var metas []pendingMeta

	err := filepath.WalkDir(layerDir, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(layerDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		base := filepath.Base(rel)

		// Whiteout → deletion marker.
		if strings.HasPrefix(base, whiteoutPrefix) {
			origName := base[len(whiteoutPrefix):]
			deletedRel := filepath.Join(filepath.Dir(rel), origName)
			if filepath.Dir(rel) == "." {
				deletedRel = origName
			}
			manifest.Deleted = append(manifest.Deleted, deletedRel)
			metas = append(metas, pendingMeta{
				header: &tar.Header{
					Name:     deletedRel,
					Typeflag: tar.TypeReg,
					Size:     0,
					PAXRecords: map[string]string{
						"TEMPORAL.deleted": "true",
					},
				},
				isDel:  true,
				delRel: deletedRel,
			})
			return nil
		}

		if d.IsDir() {
			manifest.Directories = append(manifest.Directories, rel)
			metas = append(metas, pendingMeta{
				header: &tar.Header{
					Typeflag: tar.TypeDir,
					Name:     rel + "/",
					Mode:     0o755,
				},
				isDir:  true,
				dirRel: rel,
			})
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		files = append(files, pendingFile{
			rel:  rel,
			mode: info.Mode(),
			size: info.Size(),
			path: path,
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	var chunks []*ChunkBlob
	chunkIdx := 0

	chunkID := func() string {
		return fmt.Sprintf("chunk-%04d", chunkIdx)
	}

	// Helper to write metadata entries (dirs + whiteouts) into a tar writer.
	writeMetas := func(tw *tar.Writer) error {
		for i, m := range metas {
			if err := tw.WriteHeader(m.header); err != nil {
				return err
			}
			// Clear so we don't write again.
			metas[i].header = nil
		}
		return nil
	}

	// Start first chunk with all metadata entries (they're tiny).
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hasMetaEntries := len(metas) > 0

	if hasMetaEntries {
		for _, m := range metas {
			if m.header == nil {
				continue
			}
			if err := tw.WriteHeader(m.header); err != nil {
				return nil, nil, err
			}
		}
	}
	_ = writeMetas // suppress unused; we inlined it above

	for _, f := range files {
		// Estimate overhead: tar header is ~512 bytes (more with PAX).
		entrySize := f.size + 512

		// If adding this file would exceed chunkSize and we already have content,
		// finalize current chunk and start a new one.
		if buf.Len() > 0 && int64(buf.Len())+entrySize > chunkSize {
			if err := tw.Close(); err != nil {
				return nil, nil, err
			}
			cid := chunkID()
			chunks = append(chunks, &ChunkBlob{ID: cid, Data: copyBytes(buf.Bytes())})
			manifest.Chunks[cid] = ManifestChunk{}
			chunkIdx++
			buf.Reset()
			tw = tar.NewWriter(&buf)
		}

		// Re-stat the file at write time. The size captured during WalkDir
		// may be stale if the file was modified between the walk and now
		// (e.g., git pack-objects finalizing files concurrently).
		fp, err := os.Open(f.path)
		if err != nil {
			return nil, nil, err
		}
		fi, err := fp.Stat()
		if err != nil {
			fp.Close()
			return nil, nil, err
		}
		actualSize := fi.Size()
		actualMode := fi.Mode()

		// Write file into current chunk.
		hdr := &tar.Header{
			Name: f.rel,
			Mode: int64(actualMode),
			Size: actualSize,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			fp.Close()
			return nil, nil, err
		}

		_, err = io.Copy(tw, fp)
		fp.Close()
		if err != nil {
			return nil, nil, err
		}

		// Use the actual size for manifest and chunk size decisions.
		f.size = actualSize
		f.mode = actualMode

		cid := chunkID()
		manifest.Files = append(manifest.Files, ManifestFileEntry{
			Path:    f.rel,
			Mode:    f.mode,
			Size:    f.size,
			ChunkID: cid,
		})

		// If this single file exceeds chunkSize, finalize its own chunk.
		if f.size >= chunkSize {
			if err := tw.Close(); err != nil {
				return nil, nil, err
			}
			chunks = append(chunks, &ChunkBlob{ID: cid, Data: copyBytes(buf.Bytes())})
			manifest.Chunks[cid] = ManifestChunk{}
			chunkIdx++
			buf.Reset()
			tw = tar.NewWriter(&buf)
		}
	}

	// Finalize any remaining content.
	if buf.Len() > 0 {
		if err := tw.Close(); err != nil {
			return nil, nil, err
		}
		cid := chunkID()
		chunks = append(chunks, &ChunkBlob{ID: cid, Data: copyBytes(buf.Bytes())})
		manifest.Chunks[cid] = ManifestChunk{}
	} else {
		// Close empty writer to avoid leak.
		_ = tw.Close()
	}

	// If no chunks were produced (empty diff), ensure at least the chunk map exists.
	if len(chunks) == 0 && (hasMetaEntries) {
		// The metadata was already written but buf was empty after close;
		// this shouldn't happen since we started writing metas. Handle gracefully.
	}

	return chunks, manifest, nil
}

// copyBytes returns a copy of the byte slice (buf.Bytes() shares the buffer).
func copyBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// chunkContentHash returns a hex-encoded SHA-256 hash of the chunk data.
// This can be used as a stable ID for deduplication.
func chunkContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// MergeManifests merges multiple manifests in version order (earliest first).
// Later entries override earlier ones for the same path. Deleted entries mask
// files from earlier manifests. The result is a single unified manifest
// representing the full file tree.
func MergeManifests(manifests []*Manifest) *Manifest {
	fileMap := make(map[string]ManifestFileEntry)
	dirSet := make(map[string]bool)
	deleted := make(map[string]bool)
	allChunks := make(map[string]ManifestChunk)

	for _, m := range manifests {
		// Apply deletions from this manifest.
		for _, d := range m.Deleted {
			deleted[d] = true
			delete(fileMap, d)
			delete(dirSet, d)
		}

		// Add directories.
		for _, d := range m.Directories {
			delete(deleted, d)
			dirSet[d] = true
		}

		// Add/override files.
		for _, f := range m.Files {
			delete(deleted, f.Path)
			fileMap[f.Path] = f
		}

		// Union chunks.
		for id, c := range m.Chunks {
			allChunks[id] = c
		}
	}

	result := &Manifest{
		Chunks: allChunks,
	}
	for _, f := range fileMap {
		result.Files = append(result.Files, f)
	}
	for d := range dirSet {
		result.Directories = append(result.Directories, d)
	}
	for d := range deleted {
		result.Deleted = append(result.Deleted, d)
	}
	result.buildIndex()
	return result
}

// MarshalManifest serializes a Manifest to JSON.
func MarshalManifest(m *Manifest) ([]byte, error) {
	return json.Marshal(m)
}

// UnmarshalManifest deserializes a Manifest from JSON.
func UnmarshalManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	m.buildIndex()
	return &m, nil
}

// buildIndex populates the lookup indexes for O(1) access.
// Must be called after construction, unmarshal, or merge.
func (m *Manifest) buildIndex() {
	m.fileIndex = make(map[string]int, len(m.Files))
	for i, f := range m.Files {
		m.fileIndex[f.Path] = i
	}
	m.dirIndex = make(map[string]bool, len(m.Directories))
	for _, d := range m.Directories {
		m.dirIndex[d] = true
	}
	m.deletedIndex = make(map[string]bool, len(m.Deleted))
	for _, d := range m.Deleted {
		m.deletedIndex[d] = true
	}
}

// manifestLookup returns a ManifestFileEntry for the given path, or false if not found.
func (m *Manifest) manifestLookup(rel string) (ManifestFileEntry, bool) {
	if m.fileIndex != nil {
		if idx, ok := m.fileIndex[rel]; ok {
			return m.Files[idx], true
		}
		return ManifestFileEntry{}, false
	}
	for _, f := range m.Files {
		if f.Path == rel {
			return f, true
		}
	}
	return ManifestFileEntry{}, false
}

// hasDir returns true if the manifest contains the given directory path.
func (m *Manifest) hasDir(rel string) bool {
	// Root is always implicitly present.
	if rel == "" || rel == "." {
		return true
	}
	if m.dirIndex != nil {
		return m.dirIndex[rel]
	}
	for _, d := range m.Directories {
		if d == rel {
			return true
		}
	}
	return false
}

// isDeleted returns true if the manifest records a deletion for the given path.
func (m *Manifest) isDeleted(rel string) bool {
	if m.deletedIndex != nil {
		return m.deletedIndex[rel]
	}
	for _, d := range m.Deleted {
		if d == rel {
			return true
		}
	}
	return false
}

// childEntries returns all immediate children of dir found in the manifest.
func (m *Manifest) childEntries(dir string) []string {
	seen := make(map[string]bool)
	prefix := dir
	if prefix != "" {
		prefix += "/"
	}
	for _, f := range m.Files {
		if child, ok := immediateChild(prefix, f.Path); ok {
			seen[child] = true
		}
	}
	for _, d := range m.Directories {
		if child, ok := immediateChild(prefix, d); ok {
			seen[child] = true
		}
	}
	var result []string
	for name := range seen {
		result = append(result, name)
	}
	return result
}

// immediateChild returns the first path component under prefix if path is a
// direct or nested child of prefix. For example, prefix="src/", path="src/main.go"
// returns ("main.go", true). prefix="src/", path="src/pkg/lib.go" returns ("pkg", true).
func immediateChild(prefix, path string) (string, bool) {
	if prefix == "" {
		idx := strings.IndexByte(path, '/')
		if idx < 0 {
			return path, true
		}
		return path[:idx], true
	}
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := path[len(prefix):]
	if rest == "" {
		return "", false
	}
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest, true
	}
	return rest[:idx], true
}
