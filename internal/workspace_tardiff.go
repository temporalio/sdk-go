package internal

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// TarDiffFS is a SnapshotFS implementation that uses tar archives for diffs
// and directory copies for snapshots. It requires no kernel modules and works
// on any platform. Intended for development and POC use.
//
// NOTE: Experimental
type TarDiffFS struct {
	// BasePath is the root directory under which workspace directories are created.
	BasePath string
}

var _ SnapshotFS = (*TarDiffFS)(nil)

// NewTarDiffFS creates a TarDiffFS with the given base path.
func NewTarDiffFS(basePath string) *TarDiffFS {
	return &TarDiffFS{BasePath: basePath}
}

func (t *TarDiffFS) workspacePath(wsKey string) string {
	return filepath.Join(t.BasePath, "workspaces", wsKey)
}

func (t *TarDiffFS) snapshotPath(wsKey, snapName string) string {
	return filepath.Join(t.BasePath, "snapshots", wsKey, snapName)
}

// Create creates a new workspace directory.
func (t *TarDiffFS) Create(wsKey string) (string, error) {
	wsPath := t.workspacePath(wsKey)
	// Remove existing workspace if any.
	_ = os.RemoveAll(wsPath)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		return "", err
	}
	return wsPath, nil
}

// Snapshot copies the workspace directory to a snapshot location.
func (t *TarDiffFS) Snapshot(wsKey string, snapName string) error {
	wsPath := t.workspacePath(wsKey)
	snapPath := t.snapshotPath(wsKey, snapName)

	// Remove previous snapshot with same name.
	_ = os.RemoveAll(snapPath)
	if err := os.MkdirAll(snapPath, 0o755); err != nil {
		return err
	}

	return copyDir(wsPath, snapPath)
}

// FullDiff generates a tar archive of the entire snapshot (all files).
func (t *TarDiffFS) FullDiff(wsKey string, snapName string) (io.Reader, int64, error) {
	snapPath := t.snapshotPath(wsKey, snapName)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hasEntries := false

	err := filepath.WalkDir(snapPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(snapPath, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		hasEntries = true
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     relPath + "/",
				Mode:     0o755,
			})
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: relPath,
			Mode: int64(info.Mode()),
			Size: info.Size(),
		}); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	if !hasEntries {
		// Nothing changed. Skip tw.Close() which would only write the
		// 1024-byte tar EOF marker into a buffer we're discarding.
		return nil, 0, nil
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	return &buf, int64(buf.Len()), nil
}

// IncrementalDiff generates a tar archive of files that changed between two snapshots.
func (t *TarDiffFS) IncrementalDiff(wsKey string, fromSnap, toSnap string) (io.Reader, int64, error) {
	fromPath := t.snapshotPath(wsKey, fromSnap)
	toPath := t.snapshotPath(wsKey, toSnap)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hasEntries := false

	err := filepath.WalkDir(toPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(toPath, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		// For directories, always include them.
		if d.IsDir() {
			hasEntries = true
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     relPath + "/",
				Mode:     0o755,
			})
		}

		// Check if file differs from the "from" snapshot.
		fromFile := filepath.Join(fromPath, relPath)
		toFile := path

		if filesEqual(fromFile, toFile) {
			return nil // unchanged
		}

		// File is new or changed — include in diff.
		hasEntries = true
		info, err := d.Info()
		if err != nil {
			return err
		}

		header := &tar.Header{
			Name: relPath,
			Mode: int64(info.Mode()),
			Size: info.Size(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(toFile)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})

	if err != nil {
		return nil, 0, err
	}

	// Also check for deleted files (in fromSnap but not in toSnap).
	_ = filepath.WalkDir(fromPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		relPath, _ := filepath.Rel(fromPath, path)
		toFile := filepath.Join(toPath, relPath)
		if _, err := os.Stat(toFile); os.IsNotExist(err) {
			hasEntries = true
			// File was deleted — record as empty file with special marker.
			_ = tw.WriteHeader(&tar.Header{
				Name:     relPath,
				Typeflag: tar.TypeReg,
				Size:     0,
				PAXRecords: map[string]string{
					"TEMPORAL.deleted": "true",
				},
			})
		}
		return nil
	})

	if !hasEntries {
		// Nothing changed. Skip tw.Close() which would only write the
		// 1024-byte tar EOF marker into a buffer we're discarding.
		return nil, 0, nil
	}

	if err := tw.Close(); err != nil {
		return nil, 0, err
	}

	return &buf, int64(buf.Len()), nil
}

// ApplyDiff extracts a tar diff onto the workspace directory.
func (t *TarDiffFS) ApplyDiff(wsKey string, diff io.Reader) error {
	return extractTarToDir(t.workspacePath(wsKey), diff)
}

// Rollback restores the workspace from a named snapshot.
func (t *TarDiffFS) Rollback(wsKey string, snapName string) error {
	wsPath := t.workspacePath(wsKey)
	snapPath := t.snapshotPath(wsKey, snapName)

	if err := os.RemoveAll(wsPath); err != nil {
		return err
	}
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		return err
	}
	return copyDir(snapPath, wsPath)
}

// Destroy removes all data for a workspace.
func (t *TarDiffFS) Destroy(wsKey string) error {
	_ = os.RemoveAll(t.workspacePath(wsKey))
	_ = os.RemoveAll(filepath.Join(t.BasePath, "snapshots", wsKey))
	return nil
}

// MountPath returns the workspace directory path.
func (t *TarDiffFS) MountPath(wsKey string) string {
	return t.workspacePath(wsKey)
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// filesEqual returns true if two files have identical content.
func filesEqual(a, b string) bool {
	aData, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	bData, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aData, bData)
}
