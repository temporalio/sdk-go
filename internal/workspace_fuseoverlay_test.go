package internal

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	gofusefs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipWithoutFUSE skips the test if FUSE is not available on this system.
func skipWithoutFUSE(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/dev/fuse"); err != nil {
			t.Skip("FUSE not available: /dev/fuse not found")
		}
	case "darwin":
		// macFUSE installs kext or uses the system extension.
		if _, err := exec.LookPath("mount_macfuse"); err != nil {
			// Also check for older OSXFUSE.
			if _, err := exec.LookPath("mount_osxfusefs"); err != nil {
				// Try loading the kext to detect macfuse.
				if _, err := os.Stat("/Library/Filesystems/macfuse.fs"); err != nil {
					t.Skip("macFUSE not available")
				}
			}
		}
	default:
		t.Skipf("FUSE not supported on %s", runtime.GOOS)
	}
}

// createAndMount is a test helper that creates a workspace and mounts it.
func createAndMount(t *testing.T, fs *FuseOverlayFS, wsKey string) string {
	t.Helper()
	path, err := fs.Create(wsKey)
	require.NoError(t, err)
	require.NoError(t, fs.EnsureMounted(wsKey))
	return path
}

func TestFuseOverlayFS_CreateAndMountPath(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fs.Destroy("ws-1") })

	path := createAndMount(t, fs, "ws-1")
	assert.DirExists(t, path)
	assert.Equal(t, path, fs.MountPath("ws-1"))
}

func TestFuseOverlayFS_WriteAndReadThroughMount(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fs.Destroy("ws-1") })

	path := createAndMount(t, fs, "ws-1")

	// Write through FUSE mount.
	require.NoError(t, os.WriteFile(filepath.Join(path, "hello.txt"), []byte("hello"), 0o644))

	// Read through FUSE mount.
	data, err := os.ReadFile(filepath.Join(path, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestFuseOverlayFS_SnapshotAndRollback(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fs.Destroy("ws-1") })

	path := createAndMount(t, fs, "ws-1")

	// Write a file.
	require.NoError(t, os.WriteFile(filepath.Join(path, "hello.txt"), []byte("hello"), 0o644))

	// Snapshot (unmounts if upper non-empty).
	require.NoError(t, fs.Snapshot("ws-1", "snap1"))
	require.NoError(t, fs.EnsureMounted("ws-1"))

	// Modify file through the remounted overlay.
	require.NoError(t, os.WriteFile(filepath.Join(path, "hello.txt"), []byte("modified"), 0o644))

	// Verify modification.
	data, err := os.ReadFile(filepath.Join(path, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "modified", string(data))

	// Rollback.
	require.NoError(t, fs.Rollback("ws-1", "snap1"))

	// Verify original content restored (lower has the merged state).
	data, err = os.ReadFile(filepath.Join(path, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestFuseOverlayFS_FullDiffAndApply(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() {
		fs.Destroy("ws-1")
		fs.Destroy("ws-2")
	})

	path := createAndMount(t, fs, "ws-1")

	// Snapshot empty state as v0.
	require.NoError(t, fs.Snapshot("ws-1", "v0"))

	// Write files.
	require.NoError(t, os.WriteFile(filepath.Join(path, "file1.txt"), []byte("content1"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(path, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "subdir", "file2.txt"), []byte("content2"), 0o644))

	// Snapshot v1.
	require.NoError(t, fs.Snapshot("ws-1", "v1"))

	// Generate full diff.
	diffReader, diffSize, err := fs.FullDiff("ws-1", "v1")
	require.NoError(t, err)
	assert.Greater(t, diffSize, int64(0))

	// Apply to fresh workspace.
	_, err2 := fs.Create("ws-2")
	require.NoError(t, err2)
	require.NoError(t, fs.ApplyDiff("ws-2", diffReader))

	// Verify applied state through FUSE mount.
	ws2Path := fs.MountPath("ws-2")
	data, err2 := os.ReadFile(filepath.Join(ws2Path, "file1.txt"))
	require.NoError(t, err2)
	assert.Equal(t, "content1", string(data))

	data, err2 = os.ReadFile(filepath.Join(ws2Path, "subdir", "file2.txt"))
	require.NoError(t, err2)
	assert.Equal(t, "content2", string(data))
}

func TestFuseOverlayFS_IncrementalDiffAndApply(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() {
		fs.Destroy("ws-1")
		fs.Destroy("ws-2")
	})

	path := createAndMount(t, fs, "ws-1")

	// Create initial state.
	require.NoError(t, os.WriteFile(filepath.Join(path, "file1.txt"), []byte("original"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v0"))
	require.NoError(t, fs.EnsureMounted("ws-1"))

	// Make changes.
	require.NoError(t, os.WriteFile(filepath.Join(path, "file1.txt"), []byte("changed"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(path, "file2.txt"), []byte("new file"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v1"))

	// Generate incremental diff.
	diffReader, diffSize, err := fs.IncrementalDiff("ws-1", "v0", "v1")
	require.NoError(t, err)
	assert.Greater(t, diffSize, int64(0))

	// Apply diff to a workspace that has the v0 state.
	_, err = fs.Create("ws-2")
	require.NoError(t, err)
	// Write original file1.txt to lower (simulating reconstruction from v0 diff).
	require.NoError(t, os.WriteFile(filepath.Join(fs.lowerDir("ws-2"), "file1.txt"), []byte("original"), 0o644))

	require.NoError(t, fs.ApplyDiff("ws-2", diffReader))

	// Verify applied state.
	ws2Path := fs.MountPath("ws-2")
	data, err := os.ReadFile(filepath.Join(ws2Path, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "changed", string(data))

	data, err = os.ReadFile(filepath.Join(ws2Path, "file2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new file", string(data))
}

func TestFuseOverlayFS_FileDeletion(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() {
		fs.Destroy("ws-1")
		fs.Destroy("ws-2")
	})

	path := createAndMount(t, fs, "ws-1")

	// Create files.
	require.NoError(t, os.WriteFile(filepath.Join(path, "keep.txt"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(path, "delete.txt"), []byte("delete"), 0o644))
	require.NoError(t, fs.Snapshot("ws-1", "v0"))
	require.NoError(t, fs.EnsureMounted("ws-1"))

	// Delete one file.
	require.NoError(t, os.Remove(filepath.Join(path, "delete.txt")))
	require.NoError(t, fs.Snapshot("ws-1", "v1"))

	// The snapshot layer should have a whiteout for the deleted file.
	snapDir := fs.snapshotDir("ws-1", "v1")
	_, statErr := os.Stat(filepath.Join(snapDir, ".wh.delete.txt"))
	assert.NoError(t, statErr, "whiteout file should exist in snapshot")

	// Generate incremental diff.
	diffReader, _, err := fs.IncrementalDiff("ws-1", "v0", "v1")
	require.NoError(t, err)

	// Apply to workspace with both files.
	_, err = fs.Create("ws-2")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fs.lowerDir("ws-2"), "keep.txt"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fs.lowerDir("ws-2"), "delete.txt"), []byte("delete"), 0o644))
	require.NoError(t, fs.ApplyDiff("ws-2", diffReader))

	// Verify: keep.txt still exists, delete.txt is gone.
	ws2Path := fs.MountPath("ws-2")
	data, err := os.ReadFile(filepath.Join(ws2Path, "keep.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))

	_, err = os.ReadFile(filepath.Join(ws2Path, "delete.txt"))
	assert.True(t, os.IsNotExist(err), "deleted file should not exist after applying diff")
}

func TestFuseOverlayFS_Subdirectories(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fs.Destroy("ws-1") })

	path := createAndMount(t, fs, "ws-1")

	// Create nested structure through FUSE.
	require.NoError(t, os.MkdirAll(filepath.Join(path, "src", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "src", "main.go"), []byte("package main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(path, "src", "pkg", "lib.go"), []byte("package pkg"), 0o644))

	// Snapshot.
	require.NoError(t, fs.Snapshot("ws-1", "v0"))
	require.NoError(t, fs.EnsureMounted("ws-1"))

	// Verify files are accessible after snapshot + remount.
	data, err := os.ReadFile(filepath.Join(path, "src", "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main", string(data))

	data, err = os.ReadFile(filepath.Join(path, "src", "pkg", "lib.go"))
	require.NoError(t, err)
	assert.Equal(t, "package pkg", string(data))
}

func TestFuseOverlayFS_Destroy(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)

	createAndMount(t, fs, "ws-1")
	require.NoError(t, fs.Snapshot("ws-1", "snap"))

	require.NoError(t, fs.Destroy("ws-1"))
	assert.NoDirExists(t, fs.MountPath("ws-1"))
	assert.NoDirExists(t, fs.lowerDir("ws-1"))
	assert.NoDirExists(t, fs.upperDir("ws-1"))
}

// TestFuseOverlayFS_GitClone reproduces the mmap/page-cache corruption bug:
// git clone writes pack files through FUSE, then mmaps them to read objects.
// Inside gVisor, the sentry's page cache may serve stale data for mmap reads
// because FUSE_NOTIFY_INVAL_INODE is not implemented, causing git to fail with
// "fatal: update_ref failed for ref 'HEAD': ... nonexistent object".
func TestFuseOverlayFS_GitClone(t *testing.T) {
	skipWithoutFUSE(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	base := t.TempDir()
	fusefs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fusefs.Destroy("ws-git") })

	mountPath := createAndMount(t, fusefs, "ws-git")

	// Clone a small repo through the FUSE overlay mount.
	cmd := exec.Command("git", "clone", "https://github.com/temporalio/samples-go.git")
	cmd.Dir = mountPath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "git clone failed: %s", stderr.String())

	// Verify the clone produced a valid repo.
	repoDir := filepath.Join(mountPath, "samples-go")
	assert.DirExists(t, filepath.Join(repoDir, ".git"))

	// Verify git can read objects (this is what fails with the mmap bug).
	cmd = exec.Command("git", "log", "--oneline", "-5")
	cmd.Dir = repoDir
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "git log failed: %s", stderr.String())
	assert.NotEmpty(t, out, "git log should produce output")
}

// TestFuseOverlayFS_GitCloneInGVisor is a minimal standalone reproduction of the
// gVisor FUSE mmap page-cache corruption bug. It:
// 1. Mounts a FUSE overlay filesystem
// 2. Creates a gVisor sandbox with the FUSE mount bind-mounted at /data
// 3. Runs "git clone" inside the sandbox
// 4. Verifies the clone succeeded (which fails due to mmap returning stale data)
//
// The failure is: "fatal: update_ref failed for ref 'HEAD': ... nonexistent object"
// Root cause: gVisor's sentry page cache doesn't implement FUSE_NOTIFY_INVAL_INODE,
// so mmap reads of pack files return stale/empty data after rename.
func TestFuseOverlayFS_GitCloneInGVisor(t *testing.T) {
	skipWithoutFUSE(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	runscPath := ""
	for _, p := range []string{"/tmp/runsc", "/usr/local/bin/runsc"} {
		if _, err := os.Stat(p); err == nil {
			runscPath = p
			break
		}
	}
	if p, err := exec.LookPath("runsc"); err == nil {
		runscPath = p
	}
	if runscPath == "" {
		t.Skip("runsc (gVisor) not found")
	}
	if os.Getuid() != 0 {
		t.Skip("must run as root for gVisor sandbox")
	}

	// Step 1: Mount FUSE overlay
	base := t.TempDir()
	fusefs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fusefs.Destroy("ws-gvisor") })

	mountPath := createAndMount(t, fusefs, "ws-gvisor")

	// Step 2: Create gVisor sandbox with the FUSE mount
	provider := &GVisorSandboxProvider{
		RunscPath:     runscPath,
		IgnoreCgroups: true,
	}
	ctx := context.Background()
	sb, err := provider.CreateSandbox(ctx, SandboxOptions{
		WorkspacePath: mountPath,
		NetworkPolicy: SandboxNetworkPolicy{
			AllowedHosts: []SandboxHostPort{{Host: "*.github.com", Port: 443}},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { sb.Close() })

	// Clone samples-go (larger repo, ~7MB pack) — this is the real test.
	// The sandbox has network proxy via AllowedHosts, same as Temporal worker.
	result0, err := sb.Exec(ctx, "git clone https://github.com/temporalio/samples-go.git /data/samples-go 2>&1; echo EXIT=$?", ExecOptions{})
	require.NoError(t, err)
	t.Logf("git clone samples-go: %s", result0.Stdout)
	require.Contains(t, string(result0.Stdout), "EXIT=0", "git clone samples-go failed")

	// Step 3: Basic write+read test through FUSE from inside gVisor.
	// Write a known pattern, then read it back in the SAME exec.
	writeReadCmd := `dd if=/dev/urandom bs=1M count=1 of=/data/testfile 2>/dev/null && sha1sum /data/testfile`
	result, err := sb.Exec(ctx, writeReadCmd, ExecOptions{})
	require.NoError(t, err)
	t.Logf("sandbox write+read: exit=%d stdout=%s", result.ExitCode, result.Stdout)
	require.Equal(t, 0, result.ExitCode)
	sandboxHash := string(result.Stdout)

	// Read the same file from host
	hostHash, _ := exec.Command("sha1sum", filepath.Join(mountPath, "testfile")).Output()
	t.Logf("host hash: %s", string(hostHash))
	require.Contains(t, sandboxHash, string(hostHash[:40]), "data mismatch between sandbox and host")

	// Step 4: Write in one exec, read in another (different process)
	result, err = sb.Exec(ctx, "dd if=/dev/urandom bs=1M count=1 of=/data/testfile2 2>/dev/null && sha1sum /data/testfile2", ExecOptions{})
	require.NoError(t, err)
	writeHash := string(result.Stdout)
	t.Logf("sandbox write: %s", writeHash)

	result, err = sb.Exec(ctx, "sha1sum /data/testfile2", ExecOptions{})
	require.NoError(t, err)
	readHash := string(result.Stdout)
	t.Logf("sandbox read (different exec): %s", readHash)
	require.Equal(t, writeHash, readHash, "cross-exec read mismatch")

	// Step 5: Write in one exec, rename, read renamed file in another exec
	result, err = sb.Exec(ctx, "dd if=/dev/urandom bs=256K count=1 of=/data/tmpfile 2>/dev/null && sha1sum /data/tmpfile && mv /data/tmpfile /data/renamed", ExecOptions{})
	require.NoError(t, err)
	t.Logf("sandbox write+rename: %s", result.Stdout)
	origHash := string(result.Stdout)

	result, err = sb.Exec(ctx, "sha1sum /data/renamed", ExecOptions{})
	require.NoError(t, err)
	t.Logf("sandbox read renamed (different exec): %s", result.Stdout)
	require.Equal(t, string(result.Stdout[:40]), string(origHash[:40]), "renamed file data mismatch")

	// Step 6: Write file, rename, then compare read() vs mmap() from child process
	mmapTestCmd := `
dd if=/dev/urandom bs=256K count=1 of=/data/tmpfile 2>/dev/null
mv /data/tmpfile /data/mmaptest
python3 -c "
import mmap, hashlib
# read() path
data_read = open('/data/mmaptest','rb').read()
hash_read = hashlib.sha1(data_read).hexdigest()
# mmap() path
f = open('/data/mmaptest','rb')
mm = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)
data_mmap = mm[:]
hash_mmap = hashlib.sha1(data_mmap).hexdigest()
mm.close(); f.close()
print(f'read={hash_read} mmap={hash_mmap} match={hash_read==hash_mmap} size={len(data_read)}')
" 2>&1
`
	result, err = sb.Exec(ctx, mmapTestCmd, ExecOptions{})
	require.NoError(t, err)
	t.Logf("mmap test: %s", result.Stdout)
	assert.Contains(t, string(result.Stdout), "match=True", "read vs mmap data mismatch!")

	// Step 7: Same but within a single process (child writes, parent mmaps)
	mmapChildCmd := `
python3 -c "
import os, subprocess, mmap, hashlib
# Child writes+renames
subprocess.run(['sh','-c','dd if=/dev/urandom bs=256K count=1 of=/data/tmp2 2>/dev/null && mv /data/tmp2 /data/mmaptest2'], check=True)
# Parent reads via read()
data_read = open('/data/mmaptest2','rb').read()
hash_read = hashlib.sha1(data_read).hexdigest()
# Parent reads via mmap()
f = open('/data/mmaptest2','rb')
mm = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)
data_mmap = mm[:]
hash_mmap = hashlib.sha1(data_mmap).hexdigest()
mm.close(); f.close()
print(f'read={hash_read} mmap={hash_mmap} match={hash_read==hash_mmap}')
" 2>&1
`
	result, err = sb.Exec(ctx, mmapChildCmd, ExecOptions{})
	require.NoError(t, err)
	t.Logf("mmap child test: %s", result.Stdout)
	assert.Contains(t, string(result.Stdout), "match=True", "child write + parent mmap mismatch!")

	// Step 8: Test with go-fuse loopback FS (not our overlay) to isolate the bug
	loopbackBase := t.TempDir()
	loopbackSrc := filepath.Join(loopbackBase, "src")
	loopbackMnt := filepath.Join(loopbackBase, "mnt")
	os.MkdirAll(loopbackSrc, 0o755)
	os.MkdirAll(loopbackMnt, 0o755)

	loopRoot, err := gofusefs.NewLoopbackRoot(loopbackSrc)
	require.NoError(t, err)
	loopServer, err := gofusefs.Mount(loopbackMnt, loopRoot, &gofusefs.Options{
		MountOptions: fuse.MountOptions{
			DirectMount: true,
			AllowOther:  true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { loopServer.Unmount() })

	provider2 := &GVisorSandboxProvider{RunscPath: runscPath, IgnoreCgroups: true}
	sb2, err := provider2.CreateSandbox(ctx, SandboxOptions{
		WorkspacePath: loopbackMnt,
		NetworkPolicy: SandboxNetworkPolicy{AllowedHosts: []SandboxHostPort{{Host: "*.github.com", Port: 443}}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { sb2.Close() })

	result, err = sb2.Exec(ctx, "git clone https://github.com/temporalio/money-transfer-project-template-go.git /data/tiny-repo 2>&1; echo EXIT=$?", ExecOptions{})
	require.NoError(t, err)
	t.Logf("git clone on go-fuse LOOPBACK: %s", result.Stdout)
	assert.Contains(t, string(result.Stdout), "EXIT=0", "git clone on go-fuse LOOPBACK failed")

	// Step 9: Replicate git's write pattern — many small writes, then rename+mmap
	gitWritePatternCmd := `
python3 -c "
import os, subprocess, mmap, hashlib

# Create pack dir like git
os.makedirs('/data/gw/pack', exist_ok=True)

# Child writes with many small writes (like index-pack)
subprocess.run(['python3', '-c', '''
import os
# Write 256KB in 4096-byte chunks (like git's write pattern)
fd = os.open(\"/data/gw/pack/tmp_idx\", os.O_RDWR|os.O_CREAT|os.O_EXCL, 0o444)
data = os.urandom(262144)  # 256KB
offset = 0
while offset < len(data):
    chunk = min(4096, len(data) - offset)
    os.pwrite(fd, data[offset:offset+chunk], offset)
    offset += chunk
os.fsync(fd)

# Open readonly and read back (like index-pack verification)
fd2 = os.open(\"/data/gw/pack/tmp_idx\", os.O_RDONLY)
readback = b\"\"
while True:
    chunk = os.read(fd2, 65536)
    if not chunk: break
    readback += chunk
os.close(fd2)

# Verify
import hashlib
print(f\"write_hash={hashlib.sha1(data).hexdigest()}\")
print(f\"read_hash={hashlib.sha1(readback).hexdigest()}\")
assert hashlib.sha1(data).hexdigest() == hashlib.sha1(readback).hexdigest()

# Rename
os.rename(\"/data/gw/pack/tmp_idx\", \"/data/gw/pack/pack-abc.idx\")
os.close(fd)
'''], check=True)

# Parent mmaps the renamed file (like git parent)
f = open('/data/gw/pack/pack-abc.idx', 'rb')
mm = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)
data_mmap = mm[:]
hash_mmap = hashlib.sha1(data_mmap).hexdigest()
mm.close(); f.close()

# Also read() for comparison
data_read = open('/data/gw/pack/pack-abc.idx', 'rb').read()
hash_read = hashlib.sha1(data_read).hexdigest()

print(f'mmap={hash_mmap} read={hash_read} match={hash_mmap==hash_read} size={len(data_mmap)}')
# Check if mmap data is all zeros
zeros = all(b == 0 for b in data_mmap[:4096])
print(f'first_page_zeros={zeros} first4={data_mmap[:4].hex()}')
" 2>&1
`
	result, err = sb.Exec(ctx, gitWritePatternCmd, ExecOptions{})
	require.NoError(t, err)
	t.Logf("git write pattern: %s", result.Stdout)
	assert.Contains(t, string(result.Stdout), "match=True")
}

func TestFuseOverlayFS_LowerVisibleThroughMount(t *testing.T) {
	skipWithoutFUSE(t)
	base := t.TempDir()
	fs := NewFuseOverlayFS(base)
	t.Cleanup(func() { fs.Destroy("ws-1") })

	// Create workspace (sets up dirs, does not mount).
	_, err := fs.Create("ws-1")
	require.NoError(t, err)

	// Pre-populate lower before mounting.
	require.NoError(t, os.WriteFile(filepath.Join(fs.lowerDir("ws-1"), "from-lower.txt"), []byte("lower content"), 0o644))

	// Mount and verify file from lower is visible.
	require.NoError(t, fs.EnsureMounted("ws-1"))
	data, err := os.ReadFile(filepath.Join(fs.MountPath("ws-1"), "from-lower.txt"))
	require.NoError(t, err)
	assert.Equal(t, "lower content", string(data))
}
