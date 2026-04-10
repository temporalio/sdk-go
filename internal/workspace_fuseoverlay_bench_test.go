package internal

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// skipBenchWithoutFUSE skips the benchmark if FUSE is not available.
func skipBenchWithoutFUSE(b *testing.B) {
	b.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		b.Skip("FUSE not available: /dev/fuse not found")
	}
}

// setupBenchFuse creates a FuseOverlayFS workspace and mounts it.
// Returns the mount path and a cleanup function.
func setupBenchFuse(b *testing.B) (string, func()) {
	b.Helper()
	base := b.TempDir()
	fs := NewFuseOverlayFS(base)
	path, err := fs.Create("bench")
	if err != nil {
		b.Fatal(err)
	}
	if err := fs.EnsureMounted("bench"); err != nil {
		b.Fatal(err)
	}
	return path, func() { fs.Destroy("bench") }
}

// setupBenchDirect creates a plain temp directory (no FUSE).
func setupBenchDirect(b *testing.B) (string, func()) {
	b.Helper()
	dir := b.TempDir()
	return dir, func() {}
}

// setupBenchLazy creates a workspace with files committed to storage, then
// reconstructs it lazily from manifests. Simulates a worker restart.
func setupBenchLazy(b *testing.B, numFiles, fileSize int) (string, func()) {
	b.Helper()
	base := b.TempDir()
	storeDir := filepath.Join(base, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		b.Fatal(err)
	}

	fs := NewFuseOverlayFS(filepath.Join(base, "ws"))

	// Phase 1: create workspace, write files, snapshot + generate chunks.
	if _, err := fs.Create("bench"); err != nil {
		b.Fatal(err)
	}
	if err := fs.EnsureMounted("bench"); err != nil {
		b.Fatal(err)
	}
	mountPath := fs.MountPath("bench")

	data := make([]byte, fileSize)
	rand.Read(data)
	for i := 0; i < numFiles; i++ {
		dir := filepath.Join(mountPath, fmt.Sprintf("dir%03d", i/100))
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%05d.dat", i)), data, 0o644); err != nil {
			b.Fatal(err)
		}
	}

	// Snapshot to capture the upper layer.
	if err := fs.Snapshot("bench", "v1"); err != nil {
		b.Fatal(err)
	}

	// Generate chunked manifest from the snapshot.
	layerDir := fs.snapshotDir("bench", "v1")
	chunks, manifest, err := tarOverlayLayerChunked(layerDir, defaultChunkSize)
	if err != nil {
		b.Fatal(err)
	}

	// Store chunks to local files (simulates external storage).
	chunkFiles := make(map[string]string) // chunkID → file path
	for _, chunk := range chunks {
		p := filepath.Join(storeDir, chunk.ID)
		if err := os.WriteFile(p, chunk.Data, 0o644); err != nil {
			b.Fatal(err)
		}
		manifest.Chunks[chunk.ID] = ManifestChunk{Claim: map[string]string{"path": p}}
		chunkFiles[chunk.ID] = p
	}

	// Phase 2: destroy and re-create with lazy diff.
	fs.Destroy("bench")
	if _, err := fs.Create("bench"); err != nil {
		b.Fatal(err)
	}

	loader := &benchChunkLoader{files: chunkFiles}
	if err := fs.ApplyLazyDiff("bench", manifest, loader); err != nil {
		b.Fatal(err)
	}

	return fs.MountPath("bench"), func() { fs.Destroy("bench") }
}

// benchChunkLoader loads chunks from local files for benchmarking.
type benchChunkLoader struct {
	files map[string]string // chunkID → file path
}

func (l *benchChunkLoader) WalkChunk(ctx context.Context, chunkID string, fn func(string, os.FileMode, io.Reader) error) error {
	p, ok := l.files[chunkID]
	if !ok {
		return fmt.Errorf("unknown chunk %s", chunkID)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return walkTarEntries(bytes.NewReader(data), fn)
}

// ============================================================
// Workload 1: Large Files
// ============================================================

func BenchmarkFuseLargeFileSeqWrite(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const fileSize = 100 * 1024 * 1024 // 100MB
	const blockSize = 4096
	data := make([]byte, blockSize)
	rand.Read(data)

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()
			b.SetBytes(fileSize)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := filepath.Join(dir, fmt.Sprintf("large_%d.dat", i))
				f, err := os.Create(p)
				if err != nil {
					b.Fatal(err)
				}
				for written := 0; written < fileSize; written += blockSize {
					if _, err := f.Write(data); err != nil {
						f.Close()
						b.Fatal(err)
					}
				}
				f.Close()
			}
		})
	}
}

func BenchmarkFuseLargeFileSeqRead(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const fileSize = 100 * 1024 * 1024
	const blockSize = 4096
	const numFiles = 10

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()

			// Pre-create multiple files so each iteration reads a different one (cold cache).
			for j := 0; j < numFiles; j++ {
				writeRandomFile(b, filepath.Join(dir, fmt.Sprintf("large_%d.dat", j)), fileSize)
			}

			buf := make([]byte, blockSize)
			b.SetBytes(fileSize)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := filepath.Join(dir, fmt.Sprintf("large_%d.dat", i%numFiles))
				f, err := os.Open(p)
				if err != nil {
					b.Fatal(err)
				}
				for {
					_, err := f.Read(buf)
					if err != nil {
						break
					}
				}
				f.Close()
			}
		})
	}
}

func BenchmarkFuseLargeFileRead_Lazy(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const numFiles = 25
	const fileSize = 4 * 1024 * 1024 // 4MB each, 100MB total (matches default chunk size)

	dir, cleanup := setupBenchLazy(b, numFiles, fileSize)
	defer cleanup()

	buf := make([]byte, 4096)
	b.SetBytes(int64(numFiles * fileSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < numFiles; j++ {
			p := filepath.Join(dir, fmt.Sprintf("dir%03d/file%05d.dat", j/100, j))
			f, err := os.Open(p)
			if err != nil {
				b.Fatal(err)
			}
			for {
				_, err := f.Read(buf)
				if err != nil {
					break
				}
			}
			f.Close()
		}
	}
}

// ============================================================
// Workload 2: Small Files
// ============================================================

func BenchmarkFuseSmallFileCreate(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const numFiles = 10000
	const fileSize = 1024
	data := make([]byte, fileSize)
	rand.Read(data)

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < numFiles; j++ {
					subdir := filepath.Join(dir, fmt.Sprintf("iter%d/dir%03d", i, j/100))
					os.MkdirAll(subdir, 0o755)
					os.WriteFile(filepath.Join(subdir, fmt.Sprintf("f%05d", j)), data, 0o644)
				}
			}
		})
	}
}

func BenchmarkFuseSmallFileRead(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const fileSize = 1024
	// Create enough files that b.N iterations read different files.
	// Each iteration reads one file. This measures cold-cache per-file read cost.
	const poolSize = 10000

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()

			// Pre-create files.
			data := make([]byte, fileSize)
			rand.Read(data)
			var paths []string
			for j := 0; j < poolSize; j++ {
				subdir := filepath.Join(dir, fmt.Sprintf("dir%03d", j/100))
				os.MkdirAll(subdir, 0o755)
				p := filepath.Join(subdir, fmt.Sprintf("f%05d", j))
				os.WriteFile(p, data, 0o644)
				paths = append(paths, p)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := paths[i%poolSize]
				d, err := os.ReadFile(p)
				if err != nil {
					b.Fatal(err)
				}
				if len(d) != fileSize {
					b.Fatalf("unexpected size: %d", len(d))
				}
			}
		})
	}
}

func BenchmarkFuseSmallFileRead_Lazy(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const numFiles = 1000
	const fileSize = 1024

	dir, cleanup := setupBenchLazy(b, numFiles, fileSize)
	defer cleanup()

	var paths []string
	for j := 0; j < numFiles; j++ {
		paths = append(paths, filepath.Join(dir, fmt.Sprintf("dir%03d/file%05d.dat", j/100, j)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := paths[i%numFiles]
		d, err := os.ReadFile(p)
		if err != nil {
			b.Fatal(err)
		}
		if len(d) != fileSize {
			b.Fatalf("unexpected size: %d", len(d))
		}
	}
}

func BenchmarkFuseSmallFileStat(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const fileSize = 1024
	const numFiles = 10000

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()

			// Pre-create files.
			data := make([]byte, fileSize)
			var paths []string
			for j := 0; j < numFiles; j++ {
				subdir := filepath.Join(dir, fmt.Sprintf("dir%03d", j/100))
				os.MkdirAll(subdir, 0o755)
				p := filepath.Join(subdir, fmt.Sprintf("f%05d", j))
				os.WriteFile(p, data, 0o644)
				paths = append(paths, p)
			}

			// Stat all files once per iteration. This gives accurate per-batch
			// timing — each iteration stats numFiles unique files (cold first
			// iteration, warm subsequent). Use -benchtime=1x for pure cold measurement.
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, p := range paths {
					if _, err := os.Lstat(p); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkFuseSmallFileStat_Lazy(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const numFiles = 1000
	const fileSize = 1024

	dir, cleanup := setupBenchLazy(b, numFiles, fileSize)
	defer cleanup()

	var paths []string
	for j := 0; j < numFiles; j++ {
		paths = append(paths, filepath.Join(dir, fmt.Sprintf("dir%03d/file%05d.dat", j/100, j)))
	}

	// Stat all files once per iteration.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			if _, err := os.Lstat(p); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkFuseReaddir(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const numDirs = 100
	const filesPerDir = 100

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()

			data := make([]byte, 64)
			var dirs []string
			for d := 0; d < numDirs; d++ {
				subdir := filepath.Join(dir, fmt.Sprintf("dir%03d", d))
				os.MkdirAll(subdir, 0o755)
				dirs = append(dirs, subdir)
				for f := 0; f < filesPerDir; f++ {
					os.WriteFile(filepath.Join(subdir, fmt.Sprintf("f%03d", f)), data, 0o644)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d := dirs[i%numDirs]
				entries, err := os.ReadDir(d)
				if err != nil {
					b.Fatal(err)
				}
				if len(entries) != filesPerDir {
					b.Fatalf("expected %d entries, got %d", filesPerDir, len(entries))
				}
			}
		})
	}
}

// ============================================================
// Workload 3: Mixed (simulates real agent workflow)
// ============================================================

func BenchmarkFuseMixedWorkflow(b *testing.B) {
	skipBenchWithoutFUSE(b)

	for _, mode := range []struct {
		name  string
		setup func(b *testing.B) (string, func())
	}{
		{"direct", setupBenchDirect},
		{"fuse", setupBenchFuse},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, cleanup := mode.setup(b)
			defer cleanup()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runMixedWorkload(b, dir, i)
			}
		})
	}
}

func BenchmarkFuseMixedWorkflow_Lazy(b *testing.B) {
	skipBenchWithoutFUSE(b)
	// Pre-populate with 500 source files.
	dir, cleanup := setupBenchLazy(b, 500, 4096)
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runMixedWorkload(b, dir, i)
	}
}

func runMixedWorkload(b *testing.B, dir string, iter int) {
	b.Helper()
	prefix := fmt.Sprintf("iter%d", iter)

	// Create 5 directories.
	for d := 0; d < 5; d++ {
		os.MkdirAll(filepath.Join(dir, prefix, fmt.Sprintf("pkg%d", d)), 0o755)
	}

	// Write 50 source files (1-10KB).
	var files []string
	for f := 0; f < 50; f++ {
		size := 1024 + (f%10)*1024
		data := make([]byte, size)
		p := filepath.Join(dir, prefix, fmt.Sprintf("pkg%d", f/10), fmt.Sprintf("src%02d.go", f%10))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			b.Fatal(err)
		}
		files = append(files, p)
	}

	// Read all files back.
	for _, p := range files {
		if _, err := os.ReadFile(p); err != nil {
			b.Fatal(err)
		}
	}

	// Modify 10 files.
	for _, p := range files[:10] {
		data := make([]byte, 2048)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			b.Fatal(err)
		}
	}

	// Create 5 new files.
	for f := 0; f < 5; f++ {
		p := filepath.Join(dir, prefix, fmt.Sprintf("new%d.txt", f))
		os.WriteFile(p, []byte("new content"), 0o644)
	}

	// Delete 3 files.
	for _, p := range files[47:50] {
		os.Remove(p)
	}
}

// ============================================================
// Workload 4: Overhead Isolation
// ============================================================

func BenchmarkFuseLookup(b *testing.B) {
	skipBenchWithoutFUSE(b)
	base := b.TempDir()
	ffs := NewFuseOverlayFS(base)
	defer ffs.Destroy("bench")

	if _, err := ffs.Create("bench"); err != nil {
		b.Fatal(err)
	}
	if err := ffs.EnsureMounted("bench"); err != nil {
		b.Fatal(err)
	}
	mountPath := ffs.MountPath("bench")

	// Create a file in upper.
	os.WriteFile(filepath.Join(mountPath, "upper_file"), []byte("data"), 0o644)

	// Create a file in lower (snapshot then remount).
	os.WriteFile(filepath.Join(mountPath, "lower_file"), []byte("data"), 0o644)
	if err := ffs.Snapshot("bench", "v0"); err != nil {
		b.Fatal(err)
	}
	if err := ffs.EnsureMounted("bench"); err != nil {
		b.Fatal(err)
	}
	// lower_file is now in lower, upper_file was moved to snapshot and merged to lower too.
	// Re-create upper_file in upper for the test.
	os.WriteFile(filepath.Join(mountPath, "upper_file"), []byte("data"), 0o644)

	// Create many files for cold-cache testing (different file per iteration).
	const poolSize = 1000
	var upperPaths, lowerPaths, missPaths []string
	for j := 0; j < poolSize; j++ {
		os.WriteFile(filepath.Join(mountPath, fmt.Sprintf("up_%04d", j)), []byte("data"), 0o644)
		upperPaths = append(upperPaths, filepath.Join(mountPath, fmt.Sprintf("up_%04d", j)))
		missPaths = append(missPaths, filepath.Join(mountPath, fmt.Sprintf("miss_%04d", j)))
	}
	// Snapshot to move files to lower, then create fresh upper files.
	if err := ffs.Snapshot("bench", "v1"); err != nil {
		b.Fatal(err)
	}
	if err := ffs.EnsureMounted("bench"); err != nil {
		b.Fatal(err)
	}
	for j := 0; j < poolSize; j++ {
		lowerPaths = append(lowerPaths, filepath.Join(mountPath, fmt.Sprintf("up_%04d", j)))
		os.WriteFile(filepath.Join(mountPath, fmt.Sprintf("newup_%04d", j)), []byte("data"), 0o644)
		upperPaths[j] = filepath.Join(mountPath, fmt.Sprintf("newup_%04d", j))
	}

	b.Run("upper_hit", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			os.Lstat(upperPaths[i%poolSize])
		}
	})

	b.Run("lower_hit", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			os.Lstat(lowerPaths[i%poolSize])
		}
	})

	b.Run("miss", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			os.Lstat(missPaths[i%poolSize])
		}
	})
}

func BenchmarkFuseWriteWithDiskLimit(b *testing.B) {
	skipBenchWithoutFUSE(b)
	const totalWrite = 1024 * 1024 // 1MB
	const blockSize = 4096
	data := make([]byte, blockSize)
	rand.Read(data)

	b.Run("no_limit", func(b *testing.B) {
		dir, cleanup := setupBenchFuse(b)
		defer cleanup()
		b.SetBytes(totalWrite)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p := filepath.Join(dir, fmt.Sprintf("f%d", i))
			f, _ := os.Create(p)
			for w := 0; w < totalWrite; w += blockSize {
				f.Write(data)
			}
			f.Close()
		}
	})

	b.Run("with_limit", func(b *testing.B) {
		base := b.TempDir()
		ffs := NewFuseOverlayFS(base)
		if _, err := ffs.Create("bench"); err != nil {
			b.Fatal(err)
		}
		ffs.SetDiskLimit("bench", 1024*1024*1024) // 1GB limit
		if err := ffs.EnsureMounted("bench"); err != nil {
			b.Fatal(err)
		}
		dir := ffs.MountPath("bench")
		defer ffs.Destroy("bench")

		b.SetBytes(totalWrite)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p := filepath.Join(dir, fmt.Sprintf("f%d", i))
			f, _ := os.Create(p)
			for w := 0; w < totalWrite; w += blockSize {
				f.Write(data)
			}
			f.Close()
		}
	})
}

// ============================================================
// Helpers
// ============================================================

func writeRandomFile(b *testing.B, path string, size int) {
	b.Helper()
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	rand.Read(buf)
	for written := 0; written < size; written += len(buf) {
		n := len(buf)
		if written+n > size {
			n = size - written
		}
		if _, err := f.Write(buf[:n]); err != nil {
			b.Fatal(err)
		}
	}
}
