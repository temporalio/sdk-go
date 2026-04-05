package internal

import (
	"context"
	"fmt"
	"sync"
	"time"

	sandboxpb "go.temporal.io/api/sandbox/v1"
)

// SandboxProvider creates sandboxes for executing untrusted code in isolated
// environments. Configured on WorkerOptions. When set alongside a
// WorkspaceManager, activities with sandbox options will have their commands
// executed inside a sandbox with the workspace bind-mounted at /data.
//
// NOTE: Experimental
type SandboxProvider interface {
	// CreateSandbox creates a new isolated sandbox environment. The sandbox
	// is ready for Exec calls after this returns. The caller must call
	// Close on the returned Sandbox when done.
	CreateSandbox(ctx context.Context, opts SandboxOptions) (Sandbox, error)
}

// Sandbox represents an isolated execution environment. Multiple Exec calls
// can be made against a warm sandbox (~5-10ms per exec). The sandbox must be
// closed when the activity completes, before workspace commit/rollback.
//
// NOTE: Experimental
type Sandbox interface {
	// Exec runs a command inside the sandbox. The command is executed via
	// "sh -c <cmd>" inside the sandbox environment. Multiple concurrent
	// Exec calls on the same sandbox are safe.
	Exec(ctx context.Context, cmd string, opts ExecOptions) (*ExecResult, error)

	// Close tears down the sandbox, killing any running processes and
	// cleaning up resources (OCI bundle, cgroup, proxy). Must be called
	// before workspace commit/rollback so no process holds files open.
	Close() error
}

// SandboxOptions configures a sandbox instance.
//
// NOTE: Experimental
type SandboxOptions struct {
	// WorkspacePath is the host filesystem path to bind-mount as /data
	// (read-write) inside the sandbox. Typically the workspace mount path
	// from WorkspaceManager.PrepareForActivity.
	WorkspacePath string

	// TemplateID selects the rootfs image for the sandbox (e.g. "python-3.12",
	// "node-22"). The template provides language runtimes and tools.
	// If empty, a minimal default rootfs is used.
	TemplateID string

	// ResourceLimits sets per-sandbox resource constraints via cgroups.
	// Zero values use sensible defaults (1 CPU, 512MB memory, 256 PIDs).
	ResourceLimits SandboxResourceLimits

	// NetworkPolicy controls outbound network access from the sandbox.
	// An empty AllowedHosts list means no network access.
	NetworkPolicy SandboxNetworkPolicy
}

// SandboxResourceLimits sets per-sandbox resource constraints enforced via
// cgroups in the OCI runtime spec.
//
// NOTE: Experimental
type SandboxResourceLimits struct {
	// CPUs is the CPU limit as a fraction of cores (e.g. 0.5 = half a core).
	// Zero uses the default (1.0).
	CPUs float64

	// MemoryMB is the memory limit in megabytes.
	// Zero uses the default (512).
	MemoryMB int64

	// DiskIOPS limits I/O operations per second on the workspace device.
	// Zero means no I/O limit.
	DiskIOPS int64

	// MaxPIDs limits the number of concurrent processes inside the sandbox.
	// Zero uses the default (256).
	MaxPIDs int64

	// DiskMB limits the total bytes written to the workspace (upper layer)
	// during an activity. Enforced by the FUSE overlay returning ENOSPC.
	// Zero means no limit (unlimited). Positive values set the limit in MB.
	DiskMB int64
}

// SandboxNetworkPolicy controls outbound network access from the sandbox.
// The sandbox has no direct network access. A host-side proxy enforces the
// allowlist, handling DNS resolution on the host side.
//
// NOTE: Experimental
type SandboxNetworkPolicy struct {
	// AllowedHosts is the list of host:port pairs the sandbox may connect to.
	// An empty list means no outbound network access.
	AllowedHosts []SandboxHostPort
}

// SandboxHostPort is a host:port pair for network allowlist entries.
//
// NOTE: Experimental
type SandboxHostPort struct {
	Host string
	Port int
}

// ExecOptions configures a single command execution inside a sandbox.
//
// NOTE: Experimental
type ExecOptions struct {
	// Timeout for this command. Zero means no timeout (inherits context deadline).
	Timeout time.Duration

	// WorkDir is the working directory inside the sandbox. Defaults to "/data".
	WorkDir string

	// Env is additional environment variables for this command (KEY=VALUE).
	Env []string
}

// ExecResult holds the output of a command executed inside a sandbox.
//
// NOTE: Experimental
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

const sandboxAccessorContextKey contextKey = "sandboxAccessor"

// sandboxAccessor lazily creates a sandbox on first access via GetSandbox().
// The activity handler stores this on the context with the creation inputs.
// The sandbox is created at most once (sync.Once). The handler retrieves the
// created sandbox via getSandbox() for cleanup.
type sandboxAccessor struct {
	provider   SandboxProvider
	opts       SandboxOptions
	wsAccessor *workspaceAccessor // may be nil if no workspace
	mu         sync.Mutex
	sandbox    Sandbox
	created    bool
	err        error
	closed     bool
}

func (a *sandboxAccessor) getOrCreate(ctx context.Context) (Sandbox, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, fmt.Errorf("sandbox already closed: activity attempted to access sandbox after cleanup")
	}
	if !a.created {
		if a.provider == nil {
			a.err = fmt.Errorf("activity has SandboxOptions but no SandboxProvider configured on worker")
		} else {
			// If a workspace accessor exists, ensure the workspace is prepared
			// and set its path on the sandbox options for bind-mounting as /data.
			if a.wsAccessor != nil {
				// Set disk limit on the workspace before preparing (mounting).
				// DiskMB == 0 means unlimited (no limit). Positive values set the cap.
				if a.wsAccessor.manager != nil && a.opts.ResourceLimits.DiskMB > 0 {
					a.wsAccessor.manager.SetDiskLimit(a.wsAccessor.runID, a.wsAccessor.wsInfo.GetWorkspaceId(), a.opts.ResourceLimits.DiskMB*1024*1024)
				}
				wsPath, wsErr := a.wsAccessor.getOrPrepare(ctx)
				if wsErr != nil {
					a.err = fmt.Errorf("workspace prepare for sandbox: %w", wsErr)
				} else {
					a.opts.WorkspacePath = wsPath
				}
			}
			if a.err == nil {
				a.sandbox, a.err = a.provider.CreateSandbox(ctx, a.opts)
			}
		}
		a.created = true
	}
	return a.sandbox, a.err
}

// Close closes the sandbox if it was created and prevents future creation.
// Does nothing if the sandbox was never accessed (lazy creation not triggered).
func (a *sandboxAccessor) Close() error {
	a.mu.Lock()
	a.closed = true
	sb := a.sandbox
	a.mu.Unlock()
	if sb != nil {
		return sb.Close()
	}
	return nil
}

// GetSandbox returns the Sandbox for the current activity. The sandbox is
// lazily created on the first call. Returns a non-nil error if the sandbox
// cannot be created (e.g., SandboxProvider not configured, runsc not found)
// or if the activity was not configured with SandboxOptions.
//
// NOTE: Experimental
func GetSandbox(ctx context.Context) (Sandbox, error) {
	v := ctx.Value(sandboxAccessorContextKey)
	if v == nil {
		return nil, fmt.Errorf("no sandbox configured: activity must have SandboxOptions set in workflow ActivityOptions")
	}
	return v.(*sandboxAccessor).getOrCreate(ctx)
}

// withSandboxAccessor stores a sandboxAccessor on the context for lazy creation.
func withSandboxAccessor(ctx context.Context, accessor *sandboxAccessor) context.Context {
	return context.WithValue(ctx, sandboxAccessorContextKey, accessor)
}

// sandboxOptionsToProto converts SDK SandboxOptions to proto.
func sandboxOptionsToProto(opts *SandboxOptions) *sandboxpb.SandboxOptions {
	if opts == nil {
		return nil
	}
	pb := &sandboxpb.SandboxOptions{
		TemplateId: opts.TemplateID,
	}
	if opts.ResourceLimits != (SandboxResourceLimits{}) {
		pb.ResourceLimits = &sandboxpb.SandboxResourceLimits{
			Cpus:     opts.ResourceLimits.CPUs,
			MemoryMb: opts.ResourceLimits.MemoryMB,
			DiskIops: opts.ResourceLimits.DiskIOPS,
			MaxPids:  opts.ResourceLimits.MaxPIDs,
			DiskMb:   opts.ResourceLimits.DiskMB,
		}
	}
	if len(opts.NetworkPolicy.AllowedHosts) > 0 {
		var hosts []*sandboxpb.HostPort
		for _, hp := range opts.NetworkPolicy.AllowedHosts {
			hosts = append(hosts, &sandboxpb.HostPort{Host: hp.Host, Port: int32(hp.Port)})
		}
		pb.NetworkPolicy = &sandboxpb.SandboxNetworkPolicy{AllowedHosts: hosts}
	}
	return pb
}

// sandboxOptionsFromProto converts proto SandboxOptions to SDK type.
func sandboxOptionsFromProto(pb *sandboxpb.SandboxOptions) *SandboxOptions {
	if pb == nil {
		return nil
	}
	opts := &SandboxOptions{
		TemplateID: pb.GetTemplateId(),
	}
	if rl := pb.GetResourceLimits(); rl != nil {
		opts.ResourceLimits = SandboxResourceLimits{
			CPUs:     rl.GetCpus(),
			MemoryMB: rl.GetMemoryMb(),
			DiskIOPS: rl.GetDiskIops(),
			MaxPIDs:  rl.GetMaxPids(),
			DiskMB:   rl.GetDiskMb(),
		}
	}
	if np := pb.GetNetworkPolicy(); np != nil {
		for _, hp := range np.GetAllowedHosts() {
			opts.NetworkPolicy.AllowedHosts = append(opts.NetworkPolicy.AllowedHosts, SandboxHostPort{
				Host: hp.GetHost(),
				Port: int(hp.GetPort()),
			})
		}
	}
	return opts
}
