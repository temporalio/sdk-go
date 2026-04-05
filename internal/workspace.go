package internal

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	workspacepb "go.temporal.io/api/workspace/v1"
	"go.temporal.io/sdk/converter"
)

// WorkspaceOptions configures a durable workspace for an activity.
//
// NOTE: Experimental
type WorkspaceOptions struct {
	// ID identifies the workspace within the workflow run. Multiple activities
	// can share the same workspace by using the same ID. The server creates
	// the workspace lazily on first use.
	ID string

	// AccessMode controls how this activity accesses the workspace.
	// READ_WRITE (default/unspecified) gives exclusive access — no other
	// activity can use the workspace while this one is running.
	// READ_ONLY gives shared access — multiple read-only activities can run
	// concurrently, but no read-write activity can be scheduled.
	AccessMode enumspb.WorkspaceAccessMode
}

// WorkspaceTransferMode constants for use in WorkspaceTransfer.Mode.
const (
	// WorkspaceHandoff transfers write access to the child. The parent loses
	// write access until the child completes, then re-acquires with child's changes.
	WorkspaceHandoff = enumspb.WORKSPACE_TRANSFER_MODE_HANDOFF
	// WorkspaceFork creates an independent copy. Parent and child diverge.
	WorkspaceFork = enumspb.WORKSPACE_TRANSFER_MODE_FORK
)

// WorkspaceTransfer specifies how a workspace is passed to a child workflow.
//
// NOTE: Experimental
type WorkspaceTransfer struct {
	// SourceWorkspaceID is the workspace ID being transferred.
	SourceWorkspaceID string

	// Mode controls how the workspace is shared with the child workflow.
	//   - WorkspaceHandoff: Transfer write access to child. Parent re-acquires after child completes.
	//   - WorkspaceFork: Child gets independent copy. Parent and child diverge.
	Mode enumspb.WorkspaceTransferMode

	// TargetWorkspaceID is the workspace ID for the forked copy (WorkspaceFork mode only).
	// The child workflow uses this ID to reference its independent copy.
	// Ignored for WorkspaceHandoff mode.
	TargetWorkspaceID string
}

// workspaceTransfersToProto converts SDK WorkspaceTransfer types to proto.
func workspaceTransfersToProto(transfers []WorkspaceTransfer) []*workspacepb.WorkspaceTransfer {
	result := make([]*workspacepb.WorkspaceTransfer, len(transfers))
	for i, g := range transfers {
		result[i] = &workspacepb.WorkspaceTransfer{
			SourceWorkspaceId: g.SourceWorkspaceID,
			TransferMode:         g.Mode,
			TargetWorkspaceId: g.TargetWorkspaceID,
		}
	}
	return result
}

// storageChunkLoader implements ChunkLoader using a StorageDriver to download
// chunk payloads and walk them as tar archives.
type storageChunkLoader struct {
	driver      converter.StorageDriver
	chunkClaims map[string]converter.StorageDriverClaim // chunkID → claim
}

func (l *storageChunkLoader) WalkChunk(ctx context.Context, chunkID string, fn func(path string, mode os.FileMode, r io.Reader) error) error {
	claim, ok := l.chunkClaims[chunkID]
	if !ok {
		return fmt.Errorf("unknown chunk ID %q", chunkID)
	}
	payloads, err := l.driver.Retrieve(
		converter.StorageDriverRetrieveContext{Context: ctx},
		[]converter.StorageDriverClaim{claim},
	)
	if err != nil {
		return fmt.Errorf("retrieve chunk %s: %w", chunkID, err)
	}
	if len(payloads) == 0 || len(payloads[0].GetData()) == 0 {
		return fmt.Errorf("empty chunk payload %s", chunkID)
	}
	return walkTarEntries(bytes.NewReader(payloads[0].GetData()), fn)
}

const workspaceAccessorContextKey contextKey = "workspaceAccessor"

// workspaceAccessor lazily prepares a workspace on first access via WorkspacePath().
type workspaceAccessor struct {
	manager  *WorkspaceManager // may be nil — error surfaces on first access
	runID    string
	wsInfo   *workspacepb.WorkspaceInfo
	mu       sync.Mutex
	path     string
	prepared bool
	err      error
	closed   bool
}

func (a *workspaceAccessor) getOrPrepare(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", fmt.Errorf("workspace already closed: activity attempted to access workspace after cleanup")
	}
	if !a.prepared {
		if a.manager == nil {
			a.err = fmt.Errorf("activity has workspace_id but no WorkspaceManager configured on worker")
		} else {
			if err := a.manager.EnsureReady(ctx, a.runID, a.wsInfo); err != nil {
				a.err = fmt.Errorf("workspace prepare: %w", err)
			} else {
				a.path, a.err = a.manager.PrepareForActivity(a.runID, a.wsInfo.GetWorkspaceId(), a.wsInfo.GetCommittedVersion())
				if a.err != nil {
					a.err = fmt.Errorf("workspace prepare: %w", a.err)
				}
			}
		}
		a.prepared = true
	}
	return a.path, a.err
}

// isPrepared returns true if the workspace was successfully prepared.
func (a *workspaceAccessor) isPrepared() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prepared && a.err == nil
}

// Close prevents further access to the workspace.
func (a *workspaceAccessor) Close() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
}

// WorkspacePath returns the filesystem path of the durable workspace for the
// current activity. The workspace is lazily prepared on the first call.
// Returns a non-nil error if the workspace cannot be prepared (e.g.,
// WorkspaceManager not configured) or if the activity was not configured
// with WorkspaceOptions in the workflow.
//
// NOTE: Experimental
func GetWorkspacePath(ctx context.Context) (string, error) {
	v := ctx.Value(workspaceAccessorContextKey)
	if v == nil {
		return "", fmt.Errorf("no workspace configured: activity must have WorkspaceOptions set in workflow ActivityOptions")
	}
	return v.(*workspaceAccessor).getOrPrepare(ctx)
}

// withWorkspaceAccessor stores a workspaceAccessor on the context for lazy preparation.
func withWorkspaceAccessor(ctx context.Context, accessor *workspaceAccessor) context.Context {
	return context.WithValue(ctx, workspaceAccessorContextKey, accessor)
}

// SnapshotFS is an abstraction over a copy-on-write filesystem that supports
// snapshots and incremental diffs. Implementations range from simple tar-based
// (TarDiffFS) to production options like ZFS or btrfs.
//
// NOTE: Experimental
type SnapshotFS interface {
	// Create creates a new workspace filesystem and returns the mount path.
	// wsKey is a composite key (runID/wsID) that uniquely identifies the
	// workspace on the local filesystem.
	Create(wsKey string) (mountPath string, err error)

	// Snapshot takes a named snapshot of the current workspace state.
	Snapshot(wsKey string, snapName string) error

	// FullDiff generates a complete snapshot stream for the named snapshot.
	// Returns nil reader and size 0 when no changes exist.
	FullDiff(wsKey string, snapName string) (io.Reader, int64, error)

	// IncrementalDiff generates a diff between two named snapshots.
	// Returns nil reader and size 0 when no changes exist.
	IncrementalDiff(wsKey string, fromSnap, toSnap string) (io.Reader, int64, error)

	// ApplyDiff applies a diff (full or incremental) to reconstruct workspace state.
	ApplyDiff(wsKey string, diff io.Reader) error

	// Rollback restores the workspace to a named snapshot, discarding changes after it.
	Rollback(wsKey string, snapName string) error

	// Destroy removes the workspace filesystem entirely.
	Destroy(wsKey string) error

	// MountPath returns the filesystem path for a workspace.
	MountPath(wsKey string) string
}

// WorkspaceManager coordinates filesystem snapshots and external storage for
// durable workspaces. It uses a SnapshotFS for local filesystem operations and
// a StorageDriver for persisting diffs to external storage.
//
// NOTE: Experimental
type WorkspaceManager struct {
	snapshotFS SnapshotFS
	driver     converter.StorageDriver
	// localVersions tracks the committed version each workspace is at locally.
	localVersions map[string]int64
}

// NewWorkspaceManager creates a WorkspaceManager.
func NewWorkspaceManager(snapshotFS SnapshotFS, driver converter.StorageDriver) *WorkspaceManager {
	return &WorkspaceManager{
		snapshotFS:    snapshotFS,
		driver:        driver,
		localVersions: make(map[string]int64),
	}
}

// localWorkspaceKey returns a key that uniquely identifies a workspace on the
// local filesystem. The workspace ID alone is not sufficient because different
// workflow runs may use the same workspace ID (e.g. "ws-1"). The run ID
// ensures isolation between concurrent workflows.
// isEmptyDir returns true if the directory is empty or does not exist.
func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return os.IsNotExist(err)
	}
	return len(entries) == 0
}

func localWorkspaceKey(runID, workspaceID string) string {
	return runID + "/" + workspaceID
}

// versionSnapshotName returns the snapshot name for a given version (e.g. "v0", "v1").
func versionSnapshotName(version int64) string {
	return fmt.Sprintf("v%d", version)
}

// EnsureReady ensures the workspace filesystem is at the committed version.
// If the workspace is already cached locally at the right version, this is a no-op.
// Otherwise it downloads manifests from external storage and sets up lazy
// chunk loading for on-demand file retrieval.
func (m *WorkspaceManager) EnsureReady(ctx context.Context, runID string, wsInfo *workspacepb.WorkspaceInfo) error {
	localKey := localWorkspaceKey(runID, wsInfo.GetWorkspaceId())
	targetVersion := wsInfo.GetCommittedVersion()

	// Check if we already have this workspace at the right version.
	if localVer, ok := m.localVersions[localKey]; ok && localVer == targetVersion {
		return nil // cache hit
	}

	// Create fresh workspace.
	if _, err := m.snapshotFS.Create(localKey); err != nil {
		return fmt.Errorf("workspace %s: create: %w", localKey, err)
	}

	// Collect all diffs (base + incremental) in order.
	var allDiffs []*workspacepb.DiffRecord
	if base := wsInfo.GetBaseSnapshot(); base != nil {
		allDiffs = append(allDiffs, base)
	}
	allDiffs = append(allDiffs, wsInfo.GetDiffs()...)

	if len(allDiffs) == 0 {
		m.localVersions[localKey] = targetVersion
		return nil
	}

	// Try chunked lazy mode: all diffs have manifests and SnapshotFS supports it.
	lazyFS, hasLazy := m.snapshotFS.(LazyDiffApplier)
	if hasLazy && allDiffsHaveManifest(allDiffs) {
		if err := m.applyDiffsLazyBatch(ctx, localKey, allDiffs, lazyFS); err == nil {
			m.localVersions[localKey] = targetVersion
			return nil
		}
		// Fall through to eager per-diff on error.
		if _, err := m.snapshotFS.Create(localKey); err != nil {
			return fmt.Errorf("workspace %s: re-create after batch failure: %w", localKey, err)
		}
	}

	// Eager fallback: download and apply each diff blob individually.
	for i, diff := range allDiffs {
		if err := m.applyDiffEager(ctx, localKey, diff); err != nil {
			return fmt.Errorf("workspace %s: apply diff %d: %w", localKey, i, err)
		}
	}

	m.localVersions[localKey] = targetVersion
	return nil
}

// PrepareForActivity takes a snapshot at the current version and returns the mount path.
// For version 0 (first activity on a fresh workspace), a new snapshot is created.
// For version > 0, the snapshot already exists from the previous CommitActivity and
// must not be recreated — ZFS snapshots carry a unique GUID that incremental send
// streams reference, so recreating would invalidate previously stored diffs.
// DiskLimiter is an optional interface that SnapshotFS implementations can
// support to enforce disk space limits on the workspace upper layer.
type DiskLimiter interface {
	SetDiskLimit(wsKey string, limitBytes int64)
}

// SetDiskLimit sets a disk space limit on the workspace if the underlying
// SnapshotFS supports it. Zero means unlimited, -1 means unlimited.
func (m *WorkspaceManager) SetDiskLimit(runID, wsID string, limitBytes int64) {
	if dl, ok := m.snapshotFS.(DiskLimiter); ok {
		dl.SetDiskLimit(localWorkspaceKey(runID, wsID), limitBytes)
	}
}

func (m *WorkspaceManager) PrepareForActivity(runID, wsID string, currentVersion int64) (string, error) {
	localKey := localWorkspaceKey(runID, wsID)

	// Remount if suspended after a previous CommitActivity.
	if suspendable, ok := m.snapshotFS.(SuspendableFS); ok {
		if err := suspendable.EnsureMounted(localKey); err != nil {
			return "", fmt.Errorf("workspace %s: ensure mounted: %w", localKey, err)
		}
	}

	// Take a snapshot at the current version if one doesn't exist yet.
	// For version 0 on a fresh workspace, this is the initial snapshot.
	// For version > 0 on a worker that just reconstructed from diffs (e.g.
	// child workflow or worker restart), the snapshot may not exist because
	// no prior CommitActivity ran on this worker. We always ensure the
	// snapshot exists so that RollbackActivity can restore to it.
	snapName := versionSnapshotName(currentVersion)
	if !m.snapshotExists(localKey, snapName) {
		if err := m.snapshotFS.Snapshot(localKey, snapName); err != nil {
			return "", fmt.Errorf("workspace %s: snapshot %s: %w", localKey, snapName, err)
		}
	}

	return m.snapshotFS.MountPath(localKey), nil
}

// snapshotExists checks if a snapshot exists for the given workspace and version.
func (m *WorkspaceManager) snapshotExists(localKey, snapName string) bool {
	// Use MountPath to get the base directory, then check for the snapshot.
	// TarDiffFS stores snapshots in {basePath}/snapshots/{wsKey}/{snapName}/.
	// This is a best-effort check — if the SnapshotFS doesn't use filesystem-based
	// snapshots, we default to taking a new one (which is idempotent).
	if tfs, ok := m.snapshotFS.(*TarDiffFS); ok {
		snapPath := tfs.snapshotPath(localKey, snapName)
		if info, err := os.Stat(snapPath); err == nil && info.IsDir() {
			return true
		}
		return false
	}
	// For other SnapshotFS implementations, always take a snapshot.
	return false
}

// SuspendableFS is an optional interface that SnapshotFS implementations can
// support to allow unmounting between activities to free resources (goroutines,
// file descriptors) while keeping on-disk state for fast remount.
type SuspendableFS interface {
	// Suspend unmounts the workspace but keeps all on-disk state and in-memory
	// metadata (manifests, chunk loaders). A subsequent mount() will restore
	// the workspace without reconstruction.
	Suspend(wsKey string)

	// EnsureMounted remounts the workspace if it was previously suspended.
	// No-op if already mounted.
	EnsureMounted(wsKey string) error
}

// CommitActivity takes a post-activity snapshot, generates chunked tar blobs
// and a manifest (for FuseOverlayFS), or falls back to uploading a single
// eager diff blob (for other SnapshotFS implementations like TarDiffFS).
func (m *WorkspaceManager) CommitActivity(ctx context.Context, runID, wsID string, currentVersion int64) (*workspacepb.WorkspaceCommit, error) {
	localKey := localWorkspaceKey(runID, wsID)
	newVersion := currentVersion + 1
	fromSnap := versionSnapshotName(currentVersion)
	toSnap := versionSnapshotName(newVersion)

	// Take post-activity snapshot.
	if err := m.snapshotFS.Snapshot(localKey, toSnap); err != nil {
		return nil, fmt.Errorf("workspace %s: snapshot %s: %w", localKey, toSnap, err)
	}

	// Suspend the mount on all exit paths to free goroutine + FD between activities.
	if suspendable, ok := m.snapshotFS.(SuspendableFS); ok {
		defer suspendable.Suspend(localKey)
	}

	// If the snapshot layer is empty (no files changed), skip the commit.
	// Return nil so the server does not advance the workspace version.
	if fuseFS, ok := m.snapshotFS.(*FuseOverlayFS); ok {
		if isEmptyDir(fuseFS.snapshotDir(localKey, toSnap)) {
			return nil, nil
		}
	}

	commit := &workspacepb.WorkspaceCommit{
		WorkspaceId: wsID,
		NewVersion:  newVersion,
		DriverName:  m.driver.Name(),
	}

	// Try chunked path (FuseOverlayFS).
	if chunkBlobs, manifest := m.generateManifest(localKey, toSnap); manifest != nil {
		var totalSize int64
		for _, chunk := range chunkBlobs {
			totalSize += int64(len(chunk.Data))
		}
		commit.DiffSizeBytes = totalSize

		if err := m.uploadChunksAndManifest(ctx, chunkBlobs, manifest, commit); err != nil {
			return nil, fmt.Errorf("workspace %s: upload chunks: %w", localKey, err)
		}

		m.localVersions[localKey] = newVersion
		return commit, nil
	}

	// Eager fallback for non-FuseOverlayFS: upload a single diff blob.
	var (
		diffReader io.Reader
		diffSize   int64
		err        error
	)
	if currentVersion == 0 {
		diffReader, diffSize, err = m.snapshotFS.FullDiff(localKey, toSnap)
	} else {
		diffReader, diffSize, err = m.snapshotFS.IncrementalDiff(localKey, fromSnap, toSnap)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace %s: generate diff: %w", localKey, err)
	}

	// No changes — diff functions return nil reader when no entries were written.
	if diffReader == nil {
		return nil, nil
	}

	commit.DiffSizeBytes = diffSize
	if diffSize > 0 {
		payloads, err := diffToPayloads(diffReader, diffSize)
		if err != nil {
			return nil, fmt.Errorf("workspace %s: %w", localKey, err)
		}
		claims, err := m.driver.Store(
			converter.StorageDriverStoreContext{Context: ctx},
			payloads,
		)
		if err != nil {
			return nil, fmt.Errorf("workspace %s: upload diff: %w", localKey, err)
		}
		if len(claims) > 0 {
			commit.DiffClaim = claims[0].ClaimData
		}
	}

	m.localVersions[localKey] = newVersion
	return commit, nil
}

// generateManifest builds chunked tar blobs and a manifest for the given snapshot layer.
// Returns nil if the SnapshotFS is not FuseOverlayFS.
func (m *WorkspaceManager) generateManifest(localKey, snapName string) ([]*ChunkBlob, *Manifest) {
	fuseFS, ok := m.snapshotFS.(*FuseOverlayFS)
	if !ok {
		return nil, nil
	}

	layerDir := fuseFS.snapshotDir(localKey, snapName)
	chunks, manifest, err := tarOverlayLayerChunked(layerDir, defaultChunkSize)
	if err != nil {
		return nil, nil
	}

	manifest.DriverName = m.driver.Name()
	return chunks, manifest
}

// uploadChunksAndManifest uploads each chunk blob, populates the manifest with
// claims, then uploads the manifest itself. Sets commit.ManifestClaim on success.
func (m *WorkspaceManager) uploadChunksAndManifest(
	ctx context.Context,
	chunks []*ChunkBlob,
	manifest *Manifest,
	commit *workspacepb.WorkspaceCommit,
) error {
	storeCtx := converter.StorageDriverStoreContext{Context: ctx}

	// Upload each chunk and record the claim.
	for _, chunk := range chunks {
		payloads := []*commonpb.Payload{{
			Metadata: map[string][]byte{
				"encoding": []byte("binary/workspace-chunk"),
			},
			Data: chunk.Data,
		}}
		claims, err := m.driver.Store(storeCtx, payloads)
		if err != nil {
			return fmt.Errorf("upload chunk %s: %w", chunk.ID, err)
		}
		if len(claims) > 0 {
			mc := manifest.Chunks[chunk.ID]
			mc.Claim = claims[0].ClaimData
			manifest.Chunks[chunk.ID] = mc
		}
	}

	// Upload manifest.
	manifestClaim, err := m.uploadManifest(ctx, manifest)
	if err != nil {
		return err
	}
	if manifestClaim != nil {
		commit.ManifestClaim = manifestClaim.ClaimData
	}
	return nil
}

// uploadManifest serializes and uploads a manifest to the storage driver.
func (m *WorkspaceManager) uploadManifest(ctx context.Context, manifest *Manifest) (*converter.StorageDriverClaim, error) {
	data, err := MarshalManifest(manifest)
	if err != nil {
		return nil, err
	}

	payloads := []*commonpb.Payload{{
		Metadata: map[string][]byte{
			"encoding": []byte("json/workspace-manifest"),
		},
		Data: data,
	}}

	claims, err := m.driver.Store(
		converter.StorageDriverStoreContext{Context: ctx},
		payloads,
	)
	if err != nil {
		return nil, err
	}
	if len(claims) == 0 {
		return nil, nil
	}
	return &claims[0], nil
}

// RollbackActivity rolls back the workspace to the current version snapshot.
func (m *WorkspaceManager) RollbackActivity(runID, wsID string, currentVersion int64) error {
	localKey := localWorkspaceKey(runID, wsID)
	return m.snapshotFS.Rollback(localKey, versionSnapshotName(currentVersion))
}

// ReleaseWorkspace destroys the local workspace filesystem and removes it from
// the version cache. Call this when the workflow is done with the workspace
// (e.g., workflow completion) to free FUSE mounts, goroutines, and file
// descriptors.
func (m *WorkspaceManager) ReleaseWorkspace(runID, wsID string) {
	localKey := localWorkspaceKey(runID, wsID)
	_ = m.snapshotFS.Destroy(localKey)
	delete(m.localVersions, localKey)
}

// allDiffsHaveManifest returns true if every diff has a manifest claim.
func allDiffsHaveManifest(diffs []*workspacepb.DiffRecord) bool {
	for _, d := range diffs {
		if len(d.GetManifestClaim()) == 0 {
			return false
		}
	}
	return len(diffs) > 0
}

// applyDiffsLazyBatch downloads all manifests, merges them, builds a unified
// chunk loader, and calls ApplyLazyDiff once. This avoids N unmount/remount
// cycles when reconstructing a workspace with N diffs.
func (m *WorkspaceManager) applyDiffsLazyBatch(
	ctx context.Context,
	wsID string,
	diffs []*workspacepb.DiffRecord,
	lazyFS LazyDiffApplier,
) error {
	var manifests []*Manifest
	allChunkClaims := make(map[string]converter.StorageDriverClaim)

	for _, diff := range diffs {
		manifestClaim := diff.GetManifestClaim()
		if len(manifestClaim) == 0 {
			return fmt.Errorf("diff missing manifest claim")
		}

		// Download manifest.
		claim := converter.StorageDriverClaim{ClaimData: manifestClaim}
		payloads, err := m.driver.Retrieve(
			converter.StorageDriverRetrieveContext{Context: ctx},
			[]converter.StorageDriverClaim{claim},
		)
		if err != nil {
			return err
		}
		if len(payloads) == 0 || len(payloads[0].GetData()) == 0 {
			return fmt.Errorf("empty manifest payload")
		}

		manifest, err := UnmarshalManifest(payloads[0].GetData())
		if err != nil {
			return err
		}

		// Collect chunk claims from this manifest.
		for chunkID, mc := range manifest.Chunks {
			allChunkClaims[chunkID] = converter.StorageDriverClaim{ClaimData: mc.Claim}
		}
		manifests = append(manifests, manifest)
	}

	// Merge all manifests in version order into a single file tree.
	merged := MergeManifests(manifests)

	// Build a single chunk loader with all chunk mappings.
	loader := &storageChunkLoader{
		driver:      m.driver,
		chunkClaims: allChunkClaims,
	}

	return lazyFS.ApplyLazyDiff(wsID, merged, loader)
}

// applyDiffEager downloads a full diff blob and applies it via SnapshotFS.ApplyDiff.
func (m *WorkspaceManager) applyDiffEager(ctx context.Context, wsID string, diff *workspacepb.DiffRecord) error {
	claim := converter.StorageDriverClaim{ClaimData: diff.GetClaim()}
	payloads, err := m.driver.Retrieve(
		converter.StorageDriverRetrieveContext{Context: ctx},
		[]converter.StorageDriverClaim{claim},
	)
	if err != nil {
		return err
	}
	if len(payloads) == 0 || len(payloads[0].GetData()) == 0 {
		return nil
	}
	return m.snapshotFS.ApplyDiff(wsID, bytes.NewReader(payloads[0].GetData()))
}

// diffToPayloads wraps diff data as a single Payload for the StorageDriver.
func diffToPayloads(r io.Reader, size int64) ([]*commonpb.Payload, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read diff data: %w", err)
	}
	return []*commonpb.Payload{
		{
			Metadata: map[string][]byte{
				"encoding": []byte("binary/workspace-diff"),
			},
			Data: data,
		},
	}, nil
}
