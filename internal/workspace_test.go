package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	workspacepb "go.temporal.io/api/workspace/v1"
	"go.temporal.io/sdk/converter"
)

func TestWorkspaceManager_FullLifecycle(t *testing.T) {
	skipWithoutFUSE(t)

	base := t.TempDir()
	storeDir := filepath.Join(base, "store")
	driver := converter.NewLocalFileStoreDriver(storeDir)
	snapshotFS := NewFuseOverlayFS(filepath.Join(base, "fs"))
	mgr := NewWorkspaceManager(snapshotFS, driver)

	ctx := context.Background()
	runID := "run-abc"
	wsID := "ws-test"

	// Step 1: First activity — create a file.
	wsInfo := &workspacepb.WorkspaceInfo{
		WorkspaceId:      wsID,
		CommittedVersion: 0,
	}

	require.NoError(t, mgr.EnsureReady(ctx, runID, wsInfo))
	wsPath, err := mgr.PrepareForActivity(runID, wsID, 0)
	require.NoError(t, err)

	// Activity writes a file.
	require.NoError(t, os.WriteFile(filepath.Join(wsPath, "output.txt"), []byte("step1"), 0o644))

	commit1, err := mgr.CommitActivity(ctx, runID, wsID, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), commit1.NewVersion)
	assert.Greater(t, commit1.DiffSizeBytes, int64(0))
	assert.NotEmpty(t, commit1.ManifestClaim)
	assert.Equal(t, driver.Name(), commit1.DriverName)

	// Step 2: Second activity on SAME manager (cache hit) — modify the file.
	wsInfo2 := &workspacepb.WorkspaceInfo{
		WorkspaceId:      wsID,
		CommittedVersion: 1,
		Diffs: []*workspacepb.DiffRecord{{
			FromVersion:   0,
			ToVersion:     1,
			SizeBytes:     commit1.DiffSizeBytes,
			DriverName:    commit1.DriverName,
			ManifestClaim: commit1.ManifestClaim,
		}},
	}

	require.NoError(t, mgr.EnsureReady(ctx, runID, wsInfo2)) // cache hit
	wsPath, err = mgr.PrepareForActivity(runID, wsID, 1)
	require.NoError(t, err)

	// Verify file from step 1 exists.
	data, err := os.ReadFile(filepath.Join(wsPath, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "step1", string(data))

	// Activity modifies the file.
	require.NoError(t, os.WriteFile(filepath.Join(wsPath, "output.txt"), []byte("step2"), 0o644))

	commit2, err := mgr.CommitActivity(ctx, runID, wsID, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), commit2.NewVersion)

	// Step 3: New manager (simulates different worker) — reconstruct from manifests.
	mgr2 := NewWorkspaceManager(NewFuseOverlayFS(filepath.Join(base, "fs2")), driver)

	wsInfo3 := &workspacepb.WorkspaceInfo{
		WorkspaceId:      wsID,
		CommittedVersion: 2,
		Diffs: []*workspacepb.DiffRecord{
			{
				FromVersion:   0,
				ToVersion:     1,
				SizeBytes:     commit1.DiffSizeBytes,
				DriverName:    commit1.DriverName,
				ManifestClaim: commit1.ManifestClaim,
			},
			{
				FromVersion:   1,
				ToVersion:     2,
				SizeBytes:     commit2.DiffSizeBytes,
				DriverName:    commit2.DriverName,
				ManifestClaim: commit2.ManifestClaim,
			},
		},
	}

	require.NoError(t, mgr2.EnsureReady(ctx, runID, wsInfo3))
	localKey := localWorkspaceKey(runID, wsID)
	wsPath2 := mgr2.snapshotFS.MountPath(localKey)

	// Verify reconstructed state has the latest content (lazily loaded).
	data, err = os.ReadFile(filepath.Join(wsPath2, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "step2", string(data))

	// Cleanup FUSE mounts.
	_ = snapshotFS.Destroy(localKey)
	_ = mgr2.snapshotFS.(*FuseOverlayFS).Destroy(localKey)
}

func TestWorkspaceManager_RollbackOnFailure(t *testing.T) {
	skipWithoutFUSE(t)

	base := t.TempDir()
	driver := converter.NewLocalFileStoreDriver(filepath.Join(base, "store"))
	snapshotFS := NewFuseOverlayFS(filepath.Join(base, "fs"))
	mgr := NewWorkspaceManager(snapshotFS, driver)

	ctx := context.Background()
	runID := "run-xyz"
	wsID := "ws-rollback"
	localKey := localWorkspaceKey(runID, wsID)

	wsInfo := &workspacepb.WorkspaceInfo{
		WorkspaceId:      wsID,
		CommittedVersion: 0,
	}
	require.NoError(t, mgr.EnsureReady(ctx, runID, wsInfo))

	wsPath, err := mgr.PrepareForActivity(runID, wsID, 0)
	require.NoError(t, err)

	// Activity writes a file.
	require.NoError(t, os.WriteFile(filepath.Join(wsPath, "temp.txt"), []byte("should be rolled back"), 0o644))

	// Simulate failure — rollback.
	require.NoError(t, mgr.RollbackActivity(runID, wsID, 0))

	// Verify file was removed.
	_, err = os.ReadFile(filepath.Join(wsPath, "temp.txt"))
	assert.True(t, os.IsNotExist(err))

	_ = snapshotFS.Destroy(localKey)
}

func TestWorkspaceManager_EagerFallback(t *testing.T) {
	base := t.TempDir()
	storeDir := filepath.Join(base, "store")
	driver := converter.NewLocalFileStoreDriver(storeDir)
	snapshotFS := NewTarDiffFS(filepath.Join(base, "fs"))
	mgr := NewWorkspaceManager(snapshotFS, driver)

	ctx := context.Background()
	runID := "run-abc"
	wsID := "ws-test"

	// Step 1: First activity — create a file.
	wsInfo := &workspacepb.WorkspaceInfo{
		WorkspaceId:      wsID,
		CommittedVersion: 0,
	}

	require.NoError(t, mgr.EnsureReady(ctx, runID, wsInfo))
	wsPath, err := mgr.PrepareForActivity(runID, wsID, 0)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(wsPath, "output.txt"), []byte("step1"), 0o644))

	commit1, err := mgr.CommitActivity(ctx, runID, wsID, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), commit1.NewVersion)
	assert.Greater(t, commit1.DiffSizeBytes, int64(0))
	assert.NotEmpty(t, commit1.DiffClaim) // eager path sets DiffClaim
	assert.Empty(t, commit1.ManifestClaim) // no manifest for TarDiffFS

	// Step 2: Modify the file.
	require.NoError(t, os.WriteFile(filepath.Join(wsPath, "output.txt"), []byte("step2"), 0o644))

	commit2, err := mgr.CommitActivity(ctx, runID, wsID, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), commit2.NewVersion)

	// Step 3: New manager — reconstruct from eager diffs.
	mgr2 := NewWorkspaceManager(NewTarDiffFS(filepath.Join(base, "fs2")), driver)

	wsInfo3 := &workspacepb.WorkspaceInfo{
		WorkspaceId:      wsID,
		CommittedVersion: 2,
		Diffs: []*workspacepb.DiffRecord{
			{
				FromVersion: 0,
				ToVersion:   1,
				SizeBytes:   commit1.DiffSizeBytes,
				DriverName:  commit1.DriverName,
				Claim:       commit1.DiffClaim,
			},
			{
				FromVersion: 1,
				ToVersion:   2,
				SizeBytes:   commit2.DiffSizeBytes,
				DriverName:  commit2.DriverName,
				Claim:       commit2.DiffClaim,
			},
		},
	}

	require.NoError(t, mgr2.EnsureReady(ctx, runID, wsInfo3))
	wsPath2 := mgr2.snapshotFS.MountPath(localWorkspaceKey(runID, wsID))

	data, err := os.ReadFile(filepath.Join(wsPath2, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "step2", string(data))
}

func TestWorkspacePath_NoAccessor(t *testing.T) {
	ctx := context.Background()
	path, err := GetWorkspacePath(ctx)
	assert.Empty(t, path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace configured")
}

func TestWorkspacePath_NilManager(t *testing.T) {
	ctx := context.Background()
	accessor := &workspaceAccessor{
		manager: nil,
		runID:   "run-1",
	}
	ctx = withWorkspaceAccessor(ctx, accessor)
	path, err := GetWorkspacePath(ctx)
	assert.Empty(t, path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no WorkspaceManager configured")
}

func TestWorkspacePath_Closed(t *testing.T) {
	ctx := context.Background()
	accessor := &workspaceAccessor{
		manager: nil,
		runID:   "run-1",
	}
	ctx = withWorkspaceAccessor(ctx, accessor)
	accessor.Close()
	path, err := GetWorkspacePath(ctx)
	assert.Empty(t, path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already closed")
}
