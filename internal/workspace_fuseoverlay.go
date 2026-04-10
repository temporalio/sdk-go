package internal

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sync/singleflight"
)

// LazyDiffApplier is an optional interface that SnapshotFS implementations can
// support to enable lazy diff application. Instead of downloading and extracting
// the full tar blob, only the manifest is downloaded, and individual file chunks
// are fetched on demand via the provided ChunkLoader.
type LazyDiffApplier interface {
	ApplyLazyDiff(wsKey string, manifest *Manifest, loader ChunkLoader) error
}

// FuseOverlayFS is a SnapshotFS implementation that uses a FUSE-based overlay
// filesystem for efficient snapshots and diffs. It mounts a FUSE overlay that
// combines a read-only lower directory with a writable upper directory.
//
// Key advantages over TarDiffFS:
//   - Snapshots are O(upper layer size) instead of O(total workspace size)
//   - Diffs are captured naturally in the upper layer (no full-tree comparison)
//   - Rollback is O(upper layer size) (just discard the upper layer)
//
// Requirements:
//
//   - FUSE support: libfuse/fuse3 on Linux, macFUSE on macOS
//
// Storage layout (wsKey = {runID}/{wsID}):
//
//	{BasePath}/
//	└── {wsKey}/
//	    ├── mount/           ← FUSE mount point (activity sees this)
//	    ├── lower/           ← accumulated state (merged after each snapshot)
//	    ├── upper/           ← writable overlay layer (captures changes)
//	    ├── cache/           ← lazily fetched files (lazy mode only)
//	    └── snapshots/
//	        ├── v0/          ← saved upper layer at version 0
//	        ├── v1/          ← saved upper layer at version 1
//	        └── ...
//
// NOTE: Experimental
type FuseOverlayFS struct {
	// BasePath is the root directory for workspace data.
	BasePath string

	mu           sync.Mutex
	servers      map[string]*fuse.Server
	configs      map[string]*overlayConfig // live overlay configs (for post-mount updates)
	manifests    map[string]*Manifest      // per-workspace merged manifest
	chunkLoaders map[string]ChunkLoader    // wsKey → chunk loader
	diskLimits   map[string]int64          // wsKey → disk limit in bytes (0 = unlimited)
	lowerSizes   map[string]int64          // wsKey → known lower dir size in bytes
}

var _ SnapshotFS = (*FuseOverlayFS)(nil)
var _ LazyDiffApplier = (*FuseOverlayFS)(nil)
var _ SuspendableFS = (*FuseOverlayFS)(nil)

// NewFuseOverlayFS creates a FuseOverlayFS with the given base path.
func NewFuseOverlayFS(basePath string) *FuseOverlayFS {
	return &FuseOverlayFS{
		BasePath:     basePath,
		servers:      make(map[string]*fuse.Server),
		configs:      make(map[string]*overlayConfig),
		manifests:    make(map[string]*Manifest),
		chunkLoaders: make(map[string]ChunkLoader),
		diskLimits:   make(map[string]int64),
		lowerSizes:   make(map[string]int64),
	}
}

// SetDiskLimit sets the maximum disk space (in bytes) for the workspace.
// 0 means unlimited. The limit covers both existing content (lower layer)
// and new writes (upper layer), enforced by the FUSE overlay returning ENOSPC.
func (f *FuseOverlayFS) SetDiskLimit(wsKey string, limitBytes int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limitBytes > 0 {
		f.diskLimits[wsKey] = limitBytes
	} else {
		delete(f.diskLimits, wsKey)
	}
	// If the workspace is already mounted, update the live overlay config
	// so the limit takes effect immediately (handles the case where
	// GetWorkspacePath was called before GetSandbox).
	if cfg, ok := f.configs[wsKey]; ok {
		cfg.diskMu.Lock()
		if limitBytes > 0 {
			if cfg.diskLimitBytes <= 0 {
				// First time setting limit on a live mount — compute current usage.
				lowerSize, ok := f.lowerSizes[wsKey]
				if !ok {
					lowerSize = dirSize(cfg.lowerDir)
				}
				cfg.diskUsedBytes = lowerSize + dirSize(cfg.upperDir)
			}
			cfg.diskLimitBytes = limitBytes
		} else {
			cfg.diskLimitBytes = 0
		}
		cfg.diskMu.Unlock()
	}
}

func (f *FuseOverlayFS) wsDir(wsKey string) string {
	return filepath.Join(f.BasePath, wsKey)
}

func (f *FuseOverlayFS) mountDir(wsKey string) string {
	return filepath.Join(f.BasePath, wsKey, "mount")
}

func (f *FuseOverlayFS) lowerDir(wsKey string) string {
	return filepath.Join(f.BasePath, wsKey, "lower")
}

func (f *FuseOverlayFS) upperDir(wsKey string) string {
	return filepath.Join(f.BasePath, wsKey, "upper")
}

func (f *FuseOverlayFS) cacheDir(wsKey string) string {
	return filepath.Join(f.BasePath, wsKey, "cache")
}

func (f *FuseOverlayFS) snapshotDir(wsKey, snap string) string {
	return filepath.Join(f.BasePath, wsKey, "snapshots", snap)
}

// Create sets up a new workspace directory structure. Does not mount the FUSE
// overlay — the caller is responsible for mounting via EnsureMounted after
// any lazy diff application is complete.
func (f *FuseOverlayFS) Create(wsKey string) (string, error) {
	_ = f.Destroy(wsKey)

	for _, d := range []string{f.mountDir(wsKey), f.lowerDir(wsKey), f.upperDir(wsKey)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", fmt.Errorf("fuseoverlay: mkdir %s: %w", d, err)
		}
	}

	return f.mountDir(wsKey), nil
}

func (f *FuseOverlayFS) mount(wsKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cfg := &overlayConfig{
		lowerDir:   f.lowerDir(wsKey),
		upperDir:   f.upperDir(wsKey),
		upperPaths: make(map[string]bool),
		whiteouts:  make(map[string]bool),
	}

	// Scan upper directory to populate in-memory path indexes.
	_ = filepath.WalkDir(cfg.upperDir, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(cfg.upperDir, path)
		if rel == "." {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, whiteoutPrefix) {
			dir := filepath.Dir(rel)
			origName := name[len(whiteoutPrefix):]
			if dir == "." {
				cfg.whiteouts[origName] = true
			} else {
				cfg.whiteouts[filepath.Join(dir, origName)] = true
			}
			return nil
		}
		cfg.upperPaths[rel] = true
		return nil
	})

	// Set up lazy mode if manifest and chunk loader are available.
	if m, ok := f.manifests[wsKey]; ok && m != nil {
		cfg.cacheDir = f.cacheDir(wsKey)
		cfg.manifest = m
		cfg.chunkLoader = f.chunkLoaders[wsKey]
		cfg.loadedChunks = make(map[string]bool)
		if err := os.MkdirAll(cfg.cacheDir, 0o755); err != nil {
			return fmt.Errorf("fuseoverlay: mkdir cache %s: %w", cfg.cacheDir, err)
		}
	}

	// Apply disk limit if configured. Total usage = lower + upper.
	if limit, ok := f.diskLimits[wsKey]; ok && limit > 0 {
		cfg.diskLimitBytes = limit
		lowerSize, ok := f.lowerSizes[wsKey]
		if !ok {
			// Defensive fallback: compute from disk if not tracked.
			lowerSize = dirSize(cfg.lowerDir)
		}
		cfg.diskUsedBytes = lowerSize + dirSize(cfg.upperDir)
	}

	root := &overlayNode{config: cfg}

	mp := f.mountDir(wsKey)
	// EntryTimeout: cache dentry lookups ("does this name exist?").
	// AttrTimeout: cache file attributes (size, mode, mtime).
	// NegativeTimeout: cache negative lookups ("this name doesn't exist").
	//
	// All three are safe with non-zero values because:
	// - All mutations go through the FUSE daemon which updates the kernel cache.
	// - The Linux kernel invalidates cached attrs after Write (fuse_write_update_attr
	//   calls fuse_invalidate_attr_mask with FUSE_STATX_MODSIZE), so mmap sees
	//   correct file sizes even with AttrTimeout > 0.
	// - Cleared on unmount (Snapshot/Suspend).
	cacheTimeout := time.Minute
	server, err := fs.Mount(mp, root, &fs.Options{
		EntryTimeout:    &cacheTimeout,
		AttrTimeout:     &cacheTimeout,
		NegativeTimeout: &cacheTimeout,
		MountOptions: fuse.MountOptions{
			FsName:      "temporal-filesystem",
			Name:        "temporal",
			DirectMount: true,  // Use syscall.Mount when available (root/CAP_SYS_ADMIN), fallback to fusermount3
			AllowOther:  true,  // Allow gVisor sandbox processes to access the mount
			EnableLocks: true,  // Enable kernel-level flock/fcntl locking (needed by git)
			MaxWrite:    1 << 20, // 1MB — reduces FUSE round-trips for sequential I/O (default 64KB)
		},
	})
	if err != nil {
		return fmt.Errorf("fuseoverlay: mount %s: %w", mp, err)
	}

	f.servers[wsKey] = server
	f.configs[wsKey] = cfg
	return nil
}

func (f *FuseOverlayFS) unmount(wsKey string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if s, ok := f.servers[wsKey]; ok {
		_ = s.Unmount()
		delete(f.servers, wsKey)
		delete(f.configs, wsKey)
	}
}

// Snapshot saves the current upper layer as a named snapshot and merges changes
// into lower. The FUSE mount is left unmounted — the caller is responsible for
// remounting via EnsureMounted if the mount is needed afterwards.
func (f *FuseOverlayFS) Snapshot(wsKey, snapName string) error {
	// If the upper layer is empty (no activity writes), just create the
	// snapshot marker directory. No need to unmount or merge.
	if isEmptyDir(f.upperDir(wsKey)) {
		snapDir := f.snapshotDir(wsKey, snapName)
		_ = os.RemoveAll(snapDir)
		return os.MkdirAll(snapDir, 0o755)
	}

	f.unmount(wsKey)

	snapDir := f.snapshotDir(wsKey, snapName)
	_ = os.RemoveAll(snapDir)
	if err := os.MkdirAll(filepath.Dir(snapDir), 0o755); err != nil {
		return err
	}

	// Move upper → snapshot.
	if err := os.Rename(f.upperDir(wsKey), snapDir); err != nil {
		return fmt.Errorf("fuseoverlay: move upper to snapshot: %w", err)
	}

	// Merge snapshot changes into lower so lower always holds full state.
	if err := mergeUpperIntoLower(f.lowerDir(wsKey), snapDir); err != nil {
		return fmt.Errorf("fuseoverlay: merge to lower: %w", err)
	}

	// In lazy mode, merge any cached files into lower and reset the cache dir.
	// Keep the manifest and chunk loader alive so the remounted FUSE overlay
	// can still lazy-load files that haven't been accessed yet. This is
	// critical for the PrepareForActivity snapshot on a reconstructed workspace
	// where most files haven't been fetched yet.
	cacheDir := f.cacheDir(wsKey)
	if _, err := os.Stat(cacheDir); err == nil {
		if err := mergeCacheIntoLower(f.lowerDir(wsKey), cacheDir); err != nil {
			return fmt.Errorf("fuseoverlay: merge cache to lower: %w", err)
		}
		_ = os.RemoveAll(cacheDir)
	}

	// Update the manifest to reflect changes from the upper layer:
	// - Remove entries for files that were deleted (whiteouts)
	// - Remove entries for files that now exist in lower (overwritten)
	// Without this, deleted or overwritten files would reappear from
	// the manifest on the next mount.
	f.mu.Lock()
	if m := f.manifests[wsKey]; m != nil {
		lowerDir := f.lowerDir(wsKey)
		var kept []ManifestFileEntry
		for _, entry := range m.Files {
			// If the file now exists in lower (was materialized via cache
			// merge or overwritten), the manifest entry is redundant.
			if _, err := os.Lstat(filepath.Join(lowerDir, entry.Path)); err == nil {
				continue
			}
			// If a whiteout exists in the snapshot, the file was deleted.
			dir := filepath.Dir(entry.Path)
			if dir == "." {
				dir = ""
			}
			whPath := filepath.Join(snapDir, dir, whiteoutPrefix+filepath.Base(entry.Path))
			if _, err := os.Lstat(whPath); err == nil {
				continue
			}
			kept = append(kept, entry)
		}
		m.Files = kept
		m.buildIndex()
	}
	f.lowerSizes[wsKey] = dirSize(f.lowerDir(wsKey))
	f.mu.Unlock()

	// Fresh upper for the next activity.
	return os.MkdirAll(f.upperDir(wsKey), 0o755)
}

// FullDiff generates a tar archive of the named snapshot layer. Since the first
// snapshot (v1) is taken from a workspace that started empty, the layer IS the
// full state and the tar is self-contained.
func (f *FuseOverlayFS) FullDiff(wsKey, snapName string) (io.Reader, int64, error) {
	return tarOverlayLayer(f.snapshotDir(wsKey, snapName))
}

// IncrementalDiff generates a tar archive of the toSnap layer. Each snapshot
// layer already contains only the incremental changes since the previous
// snapshot, so we just tar the layer directly.
func (f *FuseOverlayFS) IncrementalDiff(wsKey, fromSnap, toSnap string) (io.Reader, int64, error) {
	return tarOverlayLayer(f.snapshotDir(wsKey, toSnap))
}

// ApplyDiff applies a tar diff to the lower directory (used during workspace
// reconstruction). The overlay is unmounted, the tar is extracted to lower, and
// the overlay is remounted.
func (f *FuseOverlayFS) ApplyDiff(wsKey string, diff io.Reader) error {
	f.unmount(wsKey)
	if err := extractTarToDir(f.lowerDir(wsKey), diff); err != nil {
		return err
	}
	// Update lower size after extraction for disk quota tracking.
	f.mu.Lock()
	f.lowerSizes[wsKey] = dirSize(f.lowerDir(wsKey))
	f.mu.Unlock()
	return f.mount(wsKey)
}

// Rollback discards the upper layer (and cache in lazy mode) and remounts.
// Since lower always holds the full state at the last committed version,
// clearing upper restores the workspace. In lazy mode, the manifest is
// unchanged so unfetched files are still available on demand.
func (f *FuseOverlayFS) Rollback(wsKey, snapName string) error {
	f.unmount(wsKey)

	_ = os.RemoveAll(f.upperDir(wsKey))
	if err := os.MkdirAll(f.upperDir(wsKey), 0o755); err != nil {
		return err
	}

	// Clear cache but keep manifest — re-fetches happen on demand.
	_ = os.RemoveAll(f.cacheDir(wsKey))

	return f.mount(wsKey)
}

// Destroy unmounts and removes all data for a workspace.
func (f *FuseOverlayFS) Destroy(wsKey string) error {
	f.unmount(wsKey)

	// Best-effort path-based unmount for stale FUSE mounts left by a
	// previous process (e.g. after worker restart). The in-memory servers
	// map won't have an entry for these, so f.unmount above is a no-op.
	_ = syscall.Unmount(f.mountDir(wsKey), 0)

	_ = os.RemoveAll(f.wsDir(wsKey))
	f.mu.Lock()
	delete(f.manifests, wsKey)
	delete(f.chunkLoaders, wsKey)
	delete(f.lowerSizes, wsKey)
	delete(f.configs, wsKey)
	f.mu.Unlock()
	return nil
}

// MountPath returns the FUSE mount point where the activity sees the workspace.
func (f *FuseOverlayFS) MountPath(wsKey string) string {
	return f.mountDir(wsKey)
}

// Suspend unmounts the FUSE server to free its goroutine and file descriptor,
// but keeps all on-disk state (lower, upper, snapshots, cache) and in-memory
// metadata (manifests, chunk loaders) so the workspace can be remounted cheaply.
func (f *FuseOverlayFS) Suspend(wsKey string) {
	f.unmount(wsKey)
}

// EnsureMounted remounts the workspace if it was previously suspended.
// If the workspace is already mounted, this is a no-op.
func (f *FuseOverlayFS) EnsureMounted(wsKey string) error {
	f.mu.Lock()
	_, mounted := f.servers[wsKey]
	f.mu.Unlock()
	if mounted {
		return nil
	}

	// Check that the workspace dirs exist (i.e., it was suspended, not destroyed).
	if _, err := os.Stat(f.lowerDir(wsKey)); err != nil {
		return nil // not an error — workspace doesn't exist yet, Create will handle it
	}

	// Ensure upper and mount dirs exist.
	for _, d := range []string{f.mountDir(wsKey), f.upperDir(wsKey)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("fuseoverlay: mkdir %s: %w", d, err)
		}
	}

	return f.mount(wsKey)
}

// ApplyLazyDiff applies a manifest to the workspace for lazy file loading.
// Instead of downloading the full tar blob, only the manifest metadata is used
// to populate the FUSE overlay's virtual file tree. Individual file chunks are
// fetched on demand via the provided ChunkLoader.
func (f *FuseOverlayFS) ApplyLazyDiff(wsKey string, manifest *Manifest, loader ChunkLoader) error {
	f.unmount(wsKey)

	// Apply deletions and create directories from the manifest directly in lower.
	lowerDir := f.lowerDir(wsKey)
	for _, d := range manifest.Deleted {
		_ = os.RemoveAll(filepath.Join(lowerDir, d))
	}
	for _, d := range manifest.Directories {
		_ = os.MkdirAll(filepath.Join(lowerDir, d), 0o755)
	}

	// Merge manifest into existing (if any) for this workspace.
	f.mu.Lock()
	existing := f.manifests[wsKey]
	if existing != nil {
		manifest = MergeManifests([]*Manifest{existing, manifest})
	}
	f.manifests[wsKey] = manifest

	// Store the chunk loader. For batched application the loader already has
	// all chunk claims; for single diff it has just this diff's chunks.
	f.chunkLoaders[wsKey] = loader

	// Compute lower size from manifest (files on disk + virtual manifest files).
	lowerSize := dirSize(f.lowerDir(wsKey))
	for _, entry := range manifest.Files {
		lowerSize += entry.Size
	}
	f.lowerSizes[wsKey] = lowerSize
	f.mu.Unlock()

	return f.mount(wsKey)
}

// mergeCacheIntoLower copies all cached (lazily fetched) files into the lower
// directory so they become part of the persistent state.
func mergeCacheIntoLower(lowerDir, cacheDir string) error {
	return filepath.WalkDir(cacheDir, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(cacheDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		target := filepath.Join(lowerDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

// mergeUpperIntoLower applies changes from the upper layer directory into the
// lower directory: regular files are copied (added/modified), and whiteout
// files (.wh.name) cause the corresponding entry to be deleted from lower.
func mergeUpperIntoLower(lowerDir, upperDir string) error {
	return filepath.WalkDir(upperDir, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(upperDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		base := filepath.Base(rel)
		if strings.HasPrefix(base, whiteoutPrefix) {
			origName := base[len(whiteoutPrefix):]
			_ = os.RemoveAll(filepath.Join(lowerDir, filepath.Dir(rel), origName))
			return nil
		}

		target := filepath.Join(lowerDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

// tarOverlayLayer creates a tar archive of a snapshot layer directory,
// converting overlay whiteout files (.wh.name) to TEMPORAL.deleted PAX records
// for cross-SnapshotFS compatibility.
func tarOverlayLayer(layerDir string) (io.Reader, int64, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hasEntries := false

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

		hasEntries = true
		base := filepath.Base(rel)

		// Convert whiteout to TEMPORAL.deleted marker.
		if strings.HasPrefix(base, whiteoutPrefix) {
			origName := base[len(whiteoutPrefix):]
			deletedRel := filepath.Join(filepath.Dir(rel), origName)
			if filepath.Dir(rel) == "." {
				deletedRel = origName
			}
			return tw.WriteHeader(&tar.Header{
				Name:     deletedRel,
				Typeflag: tar.TypeReg,
				Size:     0,
				PAXRecords: map[string]string{
					"TEMPORAL.deleted": "true",
				},
			})
		}

		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     rel + "/",
				Mode:     0o755,
			})
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if err := tw.WriteHeader(&tar.Header{
			Name: rel,
			Mode: int64(info.Mode()),
			Size: info.Size(),
		}); err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})

	if err != nil {
		return nil, 0, err
	}
	if !hasEntries {
		// No files, dirs, or whiteouts — nothing changed. Skip tw.Close()
		// which would only write the 1024-byte tar EOF marker into a buffer
		// we're discarding. bytes.Buffer has no resources to leak.
		return nil, 0, nil
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	return &buf, int64(buf.Len()), nil
}

// extractTarToDir extracts a tar archive to a directory, handling
// TEMPORAL.deleted PAX records by removing the corresponding files.
func extractTarToDir(dir string, diff io.Reader) error {
	tr := tar.NewReader(diff)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dir, header.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)) {
			return fmt.Errorf("invalid path in diff: %s", header.Name)
		}

		if header.PAXRecords["TEMPORAL.deleted"] == "true" {
			_ = os.RemoveAll(target)
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
	}
	return nil
}

// walkTarEntries iterates over a tar archive, calling fn for each regular file
// and directory. TEMPORAL.deleted entries are skipped. For directories, r is nil.
func walkTarEntries(r io.Reader, fn func(path string, mode os.FileMode, r io.Reader) error) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.PAXRecords["TEMPORAL.deleted"] == "true" {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := fn(header.Name, os.FileMode(header.Mode), nil); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := fn(header.Name, os.FileMode(header.Mode), tr); err != nil {
				return err
			}
		}
	}
}

// whiteoutPrefix is the overlay filesystem whiteout marker prefix.
// A file named ".wh.foo" in the upper layer means "foo" has been deleted.
const whiteoutPrefix = ".wh."

// overlayConfig holds the lower (read-only) and upper (writable) directory paths
// for the FUSE overlay filesystem. When manifest is non-nil, the overlay operates
// in lazy mode: files listed in the manifest are virtually present and their
// chunks are fetched on demand via the chunkLoader.
type overlayConfig struct {
	lowerDir     string
	upperDir     string
	cacheDir     string              // lazy: cached file contents after chunk load
	manifest     *Manifest           // lazy: merged manifest (nil = eager mode)
	chunkLoader  ChunkLoader         // lazy: downloads and extracts chunk tars
	loadedChunks map[string]bool     // lazy: tracks which chunks have been loaded
	chunkMu      sync.Mutex          // lazy: protects loadedChunks
	fetchGrp     singleflight.Group  // dedup concurrent chunk loads (key = chunkID)

	// In-memory path indexes for upper layer and whiteouts. These avoid
	// os.Lstat syscalls in resolveExt for the common case. All mutations to
	// upper (Create, copyUp, Mkdir, Rename, Symlink, Unlink, Rmdir) update
	// these maps. Initialized from disk at mount time.
	upperPaths map[string]bool // paths (files + dirs) that exist in upper
	whiteouts  map[string]bool // paths that have been deleted (whiteout markers)
	pathMu     sync.RWMutex   // protects upperPaths and whiteouts

	// Disk space quota for the upper layer. 0 = unlimited, >0 = limit in bytes.
	diskLimitBytes int64
	diskUsedBytes  int64       // approximate; updated on write/create/unlink
	diskMu         sync.Mutex  // protects diskUsedBytes
}

// pathAdd marks a path as existing in the upper layer.
func (c *overlayConfig) pathAdd(rel string) {
	c.pathMu.Lock()
	c.upperPaths[rel] = true
	delete(c.whiteouts, rel)
	c.pathMu.Unlock()
}

// pathRemove marks a path as deleted (whiteout) from upper.
func (c *overlayConfig) pathRemove(rel string) {
	c.pathMu.Lock()
	delete(c.upperPaths, rel)
	c.whiteouts[rel] = true
	c.pathMu.Unlock()
}

// pathRename moves a path in the index from old to new.
func (c *overlayConfig) pathRename(oldRel, newRel string) {
	c.pathMu.Lock()
	delete(c.upperPaths, oldRel)
	c.whiteouts[oldRel] = true
	c.upperPaths[newRel] = true
	delete(c.whiteouts, newRel)
	c.pathMu.Unlock()
}

// diskCheckSpace returns ENOSPC if adding delta bytes would exceed the quota.
// If the quota is not set (0), it always succeeds.
func (c *overlayConfig) diskCheckSpace(delta int64) syscall.Errno {
	if c.diskLimitBytes <= 0 || delta <= 0 {
		return fs.OK
	}
	c.diskMu.Lock()
	defer c.diskMu.Unlock()
	if c.diskUsedBytes+delta > c.diskLimitBytes {
		return syscall.ENOSPC
	}
	return fs.OK
}

// diskAdd adjusts the used byte counter (positive for writes, negative for deletes).
func (c *overlayConfig) diskAdd(delta int64) {
	if c.diskLimitBytes <= 0 {
		return
	}
	c.diskMu.Lock()
	c.diskUsedBytes += delta
	if c.diskUsedBytes < 0 {
		c.diskUsedBytes = 0
	}
	c.diskMu.Unlock()
}

// dirSize computes the total size of regular files under dir.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d iofs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// overlayNode is a node in the FUSE overlay filesystem. All nodes (including
// the root) share the same overlayConfig pointer.
type overlayNode struct {
	fs.Inode
	config *overlayConfig
}

// Verify interface compliance at compile time.
var _ = (fs.NodeLookuper)((*overlayNode)(nil))
var _ = (fs.NodeGetattrer)((*overlayNode)(nil))
var _ = (fs.NodeReaddirer)((*overlayNode)(nil))
var _ = (fs.NodeOpener)((*overlayNode)(nil))
var _ = (fs.NodeCreater)((*overlayNode)(nil))
var _ = (fs.NodeUnlinker)((*overlayNode)(nil))
var _ = (fs.NodeMkdirer)((*overlayNode)(nil))
var _ = (fs.NodeRmdirer)((*overlayNode)(nil))
var _ = (fs.NodeRenamer)((*overlayNode)(nil))
var _ = (fs.NodeSetattrer)((*overlayNode)(nil))
var _ = (fs.NodeSymlinker)((*overlayNode)(nil))
var _ = (fs.NodeReadlinker)((*overlayNode)(nil))

func (n *overlayNode) relPath() string {
	return n.Path(n.Root())
}

// resolveResult describes where a path was found in the overlay.
type resolveResult int

const (
	resolveNotFound  resolveResult = iota
	resolveUpper                   // found in upper (writable) layer
	resolveLower                   // found in lower (read-only) layer
	resolveCache                   // found in lazy cache
	resolveManifest                // exists virtually in manifest (no content on disk)
)

// resolve returns the real filesystem path for a relative path by checking
// upper first, then cache, then lower, then manifest. Returns empty string
// if the path doesn't exist or has been whited out.
func (n *overlayNode) resolve(rel string) (realPath string, inUpper bool) {
	_, path, upper := n.resolveExt(rel)
	return path, upper
}

// resolveExt is the extended resolve that also reports cache and manifest hits.
// Uses in-memory path indexes for upper/whiteout to avoid os.Lstat syscalls.
func (n *overlayNode) resolveExt(rel string) (resolveResult, string, bool) {
	c := n.config
	if rel == "" {
		return resolveUpper, c.upperDir, true
	}

	c.pathMu.RLock()
	whited := c.whiteouts[rel]
	inUpper := c.upperPaths[rel]
	c.pathMu.RUnlock()

	// Check for whiteout — fast path via in-memory set.
	if whited {
		return resolveNotFound, "", false
	}

	// Check upper — fast path via in-memory set.
	if inUpper {
		return resolveUpper, filepath.Join(c.upperDir, rel), true
	}

	// Check lower. Lower's inodes are stable (only modified during Snapshot,
	// which remounts the FUSE and rebuilds the path index).
	lo := filepath.Join(c.lowerDir, rel)
	if _, err := os.Lstat(lo); err == nil {
		return resolveLower, lo, false
	}

	// Check cache (lazy mode).
	if c.cacheDir != "" {
		cached := filepath.Join(c.cacheDir, rel)
		if _, err := os.Lstat(cached); err == nil {
			return resolveCache, cached, false
		}
	}

	// Check manifest (lazy mode).
	if c.manifest != nil {
		if _, ok := c.manifest.manifestLookup(rel); ok {
			return resolveManifest, "", false
		}
		if c.manifest.hasDir(rel) {
			return resolveManifest, "", false
		}
	}

	return resolveNotFound, "", false
}

// loadFileFromChunk loads the chunk containing the given file and returns the
// cached path. Uses singleflight to dedup concurrent loads for the same chunk.
func (n *overlayNode) loadFileFromChunk(ctx context.Context, rel string) (string, error) {
	c := n.config
	if c.manifest == nil || c.chunkLoader == nil {
		return "", syscall.ENOENT
	}

	entry, ok := c.manifest.manifestLookup(rel)
	if !ok {
		return "", syscall.ENOENT
	}

	chunkID := entry.ChunkID
	cached := filepath.Join(c.cacheDir, rel)

	_, err, _ := c.fetchGrp.Do(chunkID, func() (interface{}, error) {
		// Check if chunk already loaded.
		c.chunkMu.Lock()
		loaded := c.loadedChunks[chunkID]
		c.chunkMu.Unlock()
		if loaded {
			return nil, nil
		}

		// Load the chunk, extracting only files that the merged manifest
		// assigns to this chunk. A chunk may contain older versions of files
		// that were superseded by a newer chunk; those are skipped.
		if err := c.loadChunkFiltered(ctx, chunkID); err != nil {
			return nil, err
		}

		c.chunkMu.Lock()
		c.loadedChunks[chunkID] = true
		c.chunkMu.Unlock()
		return nil, nil
	})
	if err != nil {
		return "", err
	}

	// Verify the file exists in cache after chunk extraction.
	if _, err := os.Lstat(cached); err != nil {
		return "", err
	}
	return cached, nil
}

// loadChunkFiltered downloads a chunk and writes only files that the merged
// manifest assigns to this chunk. Files in the tar that belong to a different
// (newer) chunk are skipped, preventing stale versions from entering the cache.
func (c *overlayConfig) loadChunkFiltered(ctx context.Context, chunkID string) error {
	return c.chunkLoader.WalkChunk(ctx, chunkID, func(path string, mode os.FileMode, r io.Reader) error {
		dst := filepath.Join(c.cacheDir, path)
		// Directory entry.
		if r == nil {
			return os.MkdirAll(dst, mode)
		}
		// Skip files that the manifest assigns to a different chunk.
		if c.manifest != nil {
			if entry, ok := c.manifest.manifestLookup(path); ok && entry.ChunkID != chunkID {
				return nil
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, r)
		f.Close()
		return copyErr
	})
}

// existsBelowUpper returns true if the path exists in any layer below upper
// (lower, cache, or manifest), meaning a whiteout is needed to mask it.
func (n *overlayNode) existsBelowUpper(rel string) bool {
	c := n.config
	if _, err := os.Lstat(filepath.Join(c.lowerDir, rel)); err == nil {
		return true
	}
	if c.cacheDir != "" {
		if _, err := os.Lstat(filepath.Join(c.cacheDir, rel)); err == nil {
			return true
		}
	}
	if c.manifest != nil {
		if _, ok := c.manifest.manifestLookup(rel); ok {
			return true
		}
		if c.manifest.hasDir(rel) {
			return true
		}
	}
	return false
}

// copyUp copies a file or directory from the lower layer (or cache/manifest)
// to the upper layer so it can be modified. Returns the upper path.
func (n *overlayNode) copyUp(ctx context.Context, rel string) (string, error) {
	c := n.config
	up := filepath.Join(c.upperDir, rel)
	// Fast check via path index before syscall.
	c.pathMu.RLock()
	inUpper := c.upperPaths[rel]
	c.pathMu.RUnlock()
	if inUpper {
		return up, nil
	}

	// Try lower first.
	lo := filepath.Join(c.lowerDir, rel)
	info, err := os.Lstat(lo)
	if err != nil {
		// Try cache.
		if c.cacheDir != "" {
			cached := filepath.Join(c.cacheDir, rel)
			info, err = os.Lstat(cached)
			if err == nil {
				lo = cached
			}
		}
		// Try manifest — load chunk first.
		if err != nil && c.manifest != nil {
			if cachedPath, fetchErr := n.loadFileFromChunk(ctx, rel); fetchErr == nil {
				lo = cachedPath
				info, err = os.Lstat(lo)
			}
		}
		if err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(filepath.Dir(up), 0o755); err != nil {
		return "", err
	}

	switch {
	case info.IsDir():
		if err := os.Mkdir(up, info.Mode().Perm()); err != nil {
			return "", err
		}
		c.pathAdd(rel)
		return up, nil
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(lo)
		if err != nil {
			return "", err
		}
		if err := os.Symlink(target, up); err != nil {
			return "", err
		}
		c.pathAdd(rel)
		return up, nil
	default:
		src, err := os.Open(lo)
		if err != nil {
			return "", err
		}
		defer src.Close()
		dst, err := os.OpenFile(up, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return "", err
		}
		defer dst.Close()
		written, err := io.Copy(dst, src)
		// The file's size is already counted in diskUsedBytes via lowerSize.
		// The copy creates a duplicate in upper. Adjust so the net effect is
		// zero: the file is now tracked as upper bytes instead of lower bytes.
		c.diskAdd(written - info.Size())
		c.pathAdd(rel)
		return up, err
	}
}

func (n *overlayNode) newChild(ctx context.Context, st *syscall.Stat_t) *fs.Inode {
	return n.NewInode(ctx, &overlayNode{config: n.config}, fs.StableAttr{
		Mode: uint32(st.Mode) & syscall.S_IFMT,
		Ino:  st.Ino,
	})
}

// ---------- Lookup ----------

func (n *overlayNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := filepath.Join(n.relPath(), name)
	result, path, _ := n.resolveExt(rel)
	if result == resolveNotFound {
		return nil, syscall.ENOENT
	}

	// Manifest entry — synthesize stat from manifest metadata.
	if result == resolveManifest {
		return n.lookupManifest(ctx, rel, out)
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&st)
	return n.newChild(ctx, &st), fs.OK
}

// lookupManifest synthesizes an inode for a path that exists only in the manifest.
func (n *overlayNode) lookupManifest(ctx context.Context, rel string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	c := n.config
	if entry, ok := c.manifest.manifestLookup(rel); ok {
		out.Attr.Mode = uint32(entry.Mode) | syscall.S_IFREG
		out.Attr.Size = uint64(entry.Size)
		out.Attr.Nlink = 1
		child := n.NewInode(ctx, &overlayNode{config: c}, fs.StableAttr{
			Mode: syscall.S_IFREG,
		})
		return child, fs.OK
	}
	if c.manifest.hasDir(rel) {
		out.Attr.Mode = 0o755 | syscall.S_IFDIR
		out.Attr.Nlink = 2
		child := n.NewInode(ctx, &overlayNode{config: c}, fs.StableAttr{
			Mode: syscall.S_IFDIR,
		})
		return child, fs.OK
	}
	return nil, syscall.ENOENT
}

// ---------- Getattr ----------

func (n *overlayNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if fh != nil {
		if fg, ok := fh.(fs.FileGetattrer); ok {
			return fg.Getattr(ctx, out)
		}
	}

	rel := n.relPath()
	result, path, _ := n.resolveExt(rel)
	if result == resolveNotFound {
		return syscall.ENOENT
	}

	if result == resolveManifest {
		c := n.config
		if entry, ok := c.manifest.manifestLookup(rel); ok {
			out.Attr.Mode = uint32(entry.Mode) | syscall.S_IFREG
			out.Attr.Size = uint64(entry.Size)
			out.Attr.Nlink = 1
			return fs.OK
		}
		if c.manifest.hasDir(rel) {
			out.Attr.Mode = 0o755 | syscall.S_IFDIR
			out.Attr.Nlink = 2
			return fs.OK
		}
		return syscall.ENOENT
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return fs.OK
}

// ---------- Opendir / Readdir ----------

// OpendirHandle provides a directory stream with real inode numbers for gVisor
// FUSE_READDIRPLUS compatibility. Uses NewLoopbackDirStream on the upper dir
// when no lower layers exist (fast path). Falls back to a merged stream with
// real inodes when lower/cache/manifest layers are present.
func (n *overlayNode) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	c := n.config
	rel := n.relPath()

	// Fast path: no lower layers, use loopback dirstream on upper directly.
	// NewLoopbackDirStream reads via getdents() which provides live updates
	// when child processes create/rename files (critical for git's index-pack).
	hasOtherLayers := false
	if _, err := os.Stat(filepath.Join(c.lowerDir, rel)); err == nil {
		hasOtherLayers = true
	}
	if !hasOtherLayers && c.cacheDir != "" {
		if _, err := os.Stat(filepath.Join(c.cacheDir, rel)); err == nil {
			hasOtherLayers = true
		}
	}
	if !hasOtherLayers && c.manifest != nil && c.manifest.hasDir(rel) {
		hasOtherLayers = true
	}
	if !hasOtherLayers {
		upperPath := filepath.Join(c.upperDir, rel)
		ds, errno := fs.NewLoopbackDirStream(upperPath)
		if errno == 0 {
			return ds, 0, 0
		}
	}

	// Slow path: merge layers. Collect entries from Readdir (which merges
	// upper/lower/cache/manifest) — it already includes real inode numbers.
	ds, errno := n.Readdir(ctx)
	if errno != 0 {
		return nil, 0, errno
	}
	return ds, 0, 0
}

func (n *overlayNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	c := n.config
	rel := n.relPath()
	entries := make(map[string]fuse.DirEntry)

	// Collect whiteouts from the in-memory index (no disk I/O needed).
	prefix := rel
	if prefix != "" {
		prefix += "/"
	}
	c.pathMu.RLock()
	whiteouts := make(map[string]bool)
	for wh := range c.whiteouts {
		if prefix == "" {
			if !strings.Contains(wh, "/") {
				whiteouts[wh] = true
			}
		} else if strings.HasPrefix(wh, prefix) {
			rest := wh[len(prefix):]
			if !strings.Contains(rest, "/") {
				whiteouts[rest] = true
			}
		}
	}
	// Collect upper entries from the in-memory index (no disk I/O needed).
	for p := range c.upperPaths {
		var name string
		if prefix == "" {
			if strings.Contains(p, "/") {
				continue
			}
			name = p
		} else if strings.HasPrefix(p, prefix) {
			rest := p[len(prefix):]
			if strings.Contains(rest, "/") {
				continue
			}
			name = rest
		} else {
			continue
		}
		// Get real inode for gVisor READDIRPLUS compatibility.
		entries[name] = dirEntryFromPath(filepath.Join(c.upperDir, prefix+name))
	}
	c.pathMu.RUnlock()

	// Read lower directory — needs disk I/O but only for entries not in upper.
	if dirents, err := os.ReadDir(filepath.Join(c.lowerDir, rel)); err == nil {
		for _, d := range dirents {
			name := d.Name()
			if whiteouts[name] {
				continue
			}
			if _, exists := entries[name]; exists {
				continue
			}
			entries[name] = dirEntryWithIno(d)
		}
	}

	// Read cache directory (lazy mode) — after lower, before manifest.
	if c.cacheDir != "" {
		if dirents, err := os.ReadDir(filepath.Join(c.cacheDir, rel)); err == nil {
			for _, d := range dirents {
				name := d.Name()
				if whiteouts[name] {
					continue
				}
				if _, exists := entries[name]; exists {
					continue
				}
				entries[name] = dirEntryWithIno(d)
			}
		}
	}

	if c.manifest != nil {
		for _, child := range c.manifest.childEntries(rel) {
			if whiteouts[child] {
				continue
			}
			if _, exists := entries[child]; exists {
				continue
			}
			// Determine mode from manifest.
			childRel := child
			if rel != "" {
				childRel = rel + "/" + child
			}
			mode := uint32(syscall.S_IFREG)
			if c.manifest.hasDir(childRel) {
				mode = syscall.S_IFDIR
			}
			entries[child] = fuse.DirEntry{Name: child, Mode: mode}
		}
	}

	result := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return fs.NewListDirStream(result), fs.OK
}

// dirEntryFromPath creates a fuse.DirEntry from a filesystem path with real inode.
func dirEntryFromPath(path string) fuse.DirEntry {
	entry := fuse.DirEntry{Name: filepath.Base(path)}
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err == nil {
		entry.Ino = st.Ino
		entry.Mode = st.Mode & syscall.S_IFMT
	}
	return entry
}

// dirEntryWithIno creates a fuse.DirEntry with real inode number from stat.
// gVisor's FUSE_READDIRPLUS needs real inode numbers to properly discover
// files created/renamed by child processes.
func dirEntryWithIno(d os.DirEntry) fuse.DirEntry {
	entry := fuse.DirEntry{Name: d.Name(), Mode: dirEntryMode(d)}
	if info, err := d.Info(); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			entry.Ino = st.Ino
		}
	}
	return entry
}

func dirEntryMode(d os.DirEntry) uint32 {
	switch {
	case d.Type().IsDir():
		return syscall.S_IFDIR
	case d.Type()&os.ModeSymlink != 0:
		return syscall.S_IFLNK
	default:
		return syscall.S_IFREG
	}
}

// ---------- Open ----------

func (n *overlayNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	rel := n.relPath()
	isWrite := flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0

	var path string
	if isWrite {
		var err error
		path, err = n.copyUp(ctx, rel)
		if err != nil {
			return nil, 0, fs.ToErrno(err)
		}
	} else {
		result, resolvedPath, _ := n.resolveExt(rel)
		switch result {
		case resolveNotFound:
			return nil, 0, syscall.ENOENT
		case resolveManifest:
			// Load chunk containing this file to cache.
			cached, err := n.loadFileFromChunk(ctx, rel)
			if err != nil {
				return nil, 0, fs.ToErrno(err)
			}
			path = cached
		default:
			path = resolvedPath
		}
	}

	// Strip O_APPEND: FUSE handles append by providing the correct offset
	// in Write calls. Keeping O_APPEND causes Go's WriteAt to fail.
	f, err := os.OpenFile(path, int(flags)&^(syscall.O_CREAT|syscall.O_APPEND), 0)
	if err != nil {
		return nil, 0, fs.ToErrno(err)
	}
	var sz int64
	if info, serr := f.Stat(); serr == nil {
		sz = info.Size()
	}
	// FOPEN_KEEP_CACHE tells the kernel to keep page cache data across opens.
	// Safe for read-only opens because file content doesn't change between
	// Open calls within the same FUSE mount. For write opens, the kernel
	// invalidates the cache automatically on Write.
	var fuseFlags uint32
	if !isWrite {
		fuseFlags = fuse.FOPEN_KEEP_CACHE
	}
	return &overlayFileHandle{file: f, config: n.config, fileSize: sz}, fuseFlags, fs.OK
}

// ---------- Create ----------

func (n *overlayNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	c := n.config
	rel := filepath.Join(n.relPath(), name)

	// Ensure parent exists in upper.
	_ = os.MkdirAll(filepath.Join(c.upperDir, n.relPath()), 0o755)

	// Remove whiteout if exists.
	_ = os.Remove(filepath.Join(c.upperDir, n.relPath(), whiteoutPrefix+name))

	path := filepath.Join(c.upperDir, rel)
	f, err := os.OpenFile(path, int(flags)&^syscall.O_APPEND|os.O_CREATE, os.FileMode(mode))
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		f.Close()
		return nil, nil, 0, fs.ToErrno(err)
	}
	out.Attr.FromStat(&st)
	c.pathAdd(rel)
	return n.newChild(ctx, &st), &overlayFileHandle{file: f, config: n.config, fileSize: 0}, 0, fs.OK
}

// createWhiteout creates a whiteout marker in the upper layer if the path
// exists in any layer below upper (lower, cache, or manifest).
func (n *overlayNode) createWhiteout(rel, name string) syscall.Errno {
	if n.existsBelowUpper(rel) {
		_ = os.MkdirAll(filepath.Join(n.config.upperDir, n.relPath()), 0o755)
		whPath := filepath.Join(n.config.upperDir, n.relPath(), whiteoutPrefix+name)
		f, err := os.Create(whPath)
		if err != nil {
			return fs.ToErrno(err)
		}
		f.Close()
	}
	return fs.OK
}

// ---------- Unlink ----------

func (n *overlayNode) Unlink(ctx context.Context, name string) syscall.Errno {
	rel := filepath.Join(n.relPath(), name)
	upperPath := filepath.Join(n.config.upperDir, rel)
	// Reclaim disk quota for the removed file.
	if info, err := os.Lstat(upperPath); err == nil && !info.IsDir() {
		n.config.diskAdd(-info.Size())
	}
	_ = os.Remove(upperPath)
	n.config.pathRemove(rel)
	return n.createWhiteout(rel, name)
}

// ---------- Mkdir ----------

func (n *overlayNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	c := n.config
	rel := filepath.Join(n.relPath(), name)

	_ = os.MkdirAll(filepath.Join(c.upperDir, n.relPath()), 0o755)
	_ = os.Remove(filepath.Join(c.upperDir, n.relPath(), whiteoutPrefix+name))

	path := filepath.Join(c.upperDir, rel)
	if err := os.Mkdir(path, os.FileMode(mode)); err != nil {
		return nil, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&st)
	c.pathAdd(rel)
	return n.newChild(ctx, &st), fs.OK
}

// ---------- Rmdir ----------

func (n *overlayNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	rel := filepath.Join(n.relPath(), name)
	_ = os.RemoveAll(filepath.Join(n.config.upperDir, rel))
	n.config.pathRemove(rel)
	return n.createWhiteout(rel, name)
}

// ---------- Rename ----------

func (n *overlayNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	c := n.config
	oldRel := filepath.Join(n.relPath(), name)
	newParentNode := newParent.(*overlayNode)
	newRel := filepath.Join(newParentNode.relPath(), newName)

	// Ensure source is in upper.
	srcPath, err := n.copyUp(ctx, oldRel)
	if err != nil {
		return fs.ToErrno(err)
	}

	// Ensure destination parent exists in upper.
	_ = os.MkdirAll(filepath.Join(c.upperDir, newParentNode.relPath()), 0o755)

	// Remove whiteout at destination.
	_ = os.Remove(filepath.Join(c.upperDir, newParentNode.relPath(), whiteoutPrefix+newName))

	dstPath := filepath.Join(c.upperDir, newRel)
	if err := os.Rename(srcPath, dstPath); err != nil {
		return fs.ToErrno(err)
	}

	c.pathRename(oldRel, newRel)
	// Create whiteout at old location if source existed below upper.
	return n.createWhiteout(oldRel, name)
}

// ---------- Setattr ----------

func (n *overlayNode) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	path, err := n.copyUp(ctx, n.relPath())
	if err != nil {
		return fs.ToErrno(err)
	}

	if mode, ok := in.GetMode(); ok {
		if err := syscall.Chmod(path, mode); err != nil {
			return fs.ToErrno(err)
		}
	}

	uid, uidOk := in.GetUID()
	gid, gidOk := in.GetGID()
	if uidOk || gidOk {
		u, g := -1, -1
		if uidOk {
			u = int(uid)
		}
		if gidOk {
			g = int(gid)
		}
		if err := syscall.Lchown(path, u, g); err != nil {
			return fs.ToErrno(err)
		}
	}

	if sz, ok := in.GetSize(); ok {
		// Track size change for disk quota.
		var oldSize int64
		if st, serr := os.Lstat(path); serr == nil {
			oldSize = st.Size()
		}
		delta := int64(sz) - oldSize
		if delta > 0 {
			if errno := n.config.diskCheckSpace(delta); errno != fs.OK {
				return errno
			}
		}
		if ofh, ok := fh.(*overlayFileHandle); ok {
			if err := syscall.Ftruncate(int(ofh.file.Fd()), int64(sz)); err != nil {
				return fs.ToErrno(err)
			}
		} else {
			if err := syscall.Truncate(path, int64(sz)); err != nil {
				return fs.ToErrno(err)
			}
		}
		n.config.diskAdd(delta)
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return fs.OK
}

// ---------- Symlink ----------

func (n *overlayNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	c := n.config
	rel := filepath.Join(n.relPath(), name)

	_ = os.MkdirAll(filepath.Join(c.upperDir, n.relPath()), 0o755)
	_ = os.Remove(filepath.Join(c.upperDir, n.relPath(), whiteoutPrefix+name))

	path := filepath.Join(c.upperDir, rel)
	if err := os.Symlink(target, path); err != nil {
		return nil, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&st)
	c.pathAdd(rel)
	return n.newChild(ctx, &st), fs.OK
}

// ---------- Readlink ----------

func (n *overlayNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	path, _ := n.resolve(n.relPath())
	if path == "" {
		return nil, syscall.ENOENT
	}
	target, err := os.Readlink(path)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	return []byte(target), fs.OK
}

// ---------- File Handle ----------

// overlayFileHandle wraps an os.File for FUSE read/write operations.
type overlayFileHandle struct {
	file     *os.File
	config   *overlayConfig // for disk quota tracking (may be nil)
	fileSize int64          // tracked size — avoids fstat on every Write for disk quota
}

var _ = (fs.FileReader)((*overlayFileHandle)(nil))
var _ = (fs.FileWriter)((*overlayFileHandle)(nil))
var _ = (fs.FileFlusher)((*overlayFileHandle)(nil))
var _ = (fs.FileFsyncer)((*overlayFileHandle)(nil))
var _ = (fs.FileReleaser)((*overlayFileHandle)(nil))
var _ = (fs.FileGetattrer)((*overlayFileHandle)(nil))

func (fh *overlayFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Use ReadResultFd for zero-copy reads: the kernel splices data directly
	// from the file's page cache into the FUSE response, avoiding a user-space
	// buffer copy. This works because our files are regular seekable files.
	return fuse.ReadResultFd(fh.file.Fd(), off, len(dest)), fs.OK
}

func (fh *overlayFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if fh.config != nil && fh.config.diskLimitBytes > 0 {
		// Track growth using in-memory file size to avoid fstat syscall per write.
		end := off + int64(len(data))
		var growth int64
		if end > fh.fileSize {
			growth = end - fh.fileSize
		}
		if errno := fh.config.diskCheckSpace(growth); errno != fs.OK {
			return 0, errno
		}
		n, err := fh.file.WriteAt(data, off)
		if end > fh.fileSize {
			fh.fileSize = end
		}
		fh.config.diskAdd(growth)
		if err != nil {
			return uint32(n), fs.ToErrno(err)
		}
		return uint32(n), fs.OK
	}
	n, err := fh.file.WriteAt(data, off)
	if err != nil {
		return uint32(n), fs.ToErrno(err)
	}
	return uint32(n), fs.OK
}

func (fh *overlayFileHandle) Flush(ctx context.Context) syscall.Errno {
	// Don't fsync on close — standard POSIX behavior. Data is in the page
	// cache from Write calls. Explicit fsync(2) still works via Fsync handler.
	// Workspace commit happens after activity completion, ensuring durability.
	return fs.OK
}

func (fh *overlayFileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	return fs.ToErrno(fh.file.Sync())
}

func (fh *overlayFileHandle) Release(ctx context.Context) syscall.Errno {
	return fs.ToErrno(fh.file.Close())
}

func (fh *overlayFileHandle) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(fh.file.Fd()), &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return fs.OK
}
