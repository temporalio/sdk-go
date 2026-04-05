package internal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GVisorSandboxProvider creates gVisor-based sandboxes using runsc.
// It implements SandboxProvider.
//
// NOTE: Experimental
type GVisorSandboxProvider struct {
	// RunscPath is the path to the runsc binary.
	RunscPath string

	// BundleBaseDir is the base directory for OCI bundle directories.
	// Each sandbox gets a subdirectory. Defaults to os.TempDir()/temporal-sandboxes.
	BundleBaseDir string

	// RunscRoot is the root directory for runsc container state.
	// Defaults to os.TempDir()/temporal-runsc.
	RunscRoot string

	// DefaultRootfs is the path to the default root filesystem directory
	// used when SandboxOptions.TemplateID is empty. Must contain at least
	// /bin/sh. If empty, defaults to "/" (host rootfs, read-only).
	DefaultRootfs string

	// TemplateStore resolves template IDs to rootfs paths. Optional.
	// If nil, only DefaultRootfs is used.
	TemplateStore TemplateStore

	// IgnoreCgroups disables cgroup resource limits. Useful in environments
	// where /sys/fs/cgroup is read-only (e.g. unprivileged containers).
	// When true, ResourceLimits in SandboxOptions are ignored.
	IgnoreCgroups bool

	// Disable9PGofer, when true, forces gVisor to use the 9P gofer
	// protocol instead of direct filesystem access. Direct filesystem
	// access (directfs) is the default and is required when the
	// workspace is on a FUSE mount — the 9P gofer does not support
	// getcwd(2) or fsync(2) through FUSE, breaking tools like Go and
	// git. Leave false (the default) for correct FUSE support.
	Disable9PGofer bool

	// HostNetwork passes --network=host to runsc instead of --network=none.
	// By default (false), the sandbox uses --network=none with --host-uds=open,
	// which provides strong network isolation: the only exit is the
	// bind-mounted Unix socket proxy. All direct TCP, DNS, and raw socket
	// access is blocked.
	//
	// Set to true as a fallback for environments where --host-uds=open
	// is not supported (older gVisor versions). In this mode, the proxy
	// listens on a TCP port and tools that bypass HTTP_PROXY can connect
	// directly.
	HostNetwork bool

	// ExtraRunscFlags are additional flags passed to every runsc invocation
	// (e.g. ["--platform=ptrace"]).
	ExtraRunscFlags []string
}

// TemplateStore resolves sandbox template IDs to host rootfs directory paths.
//
// NOTE: Experimental
type TemplateStore interface {
	// RootfsPath returns the host path to the rootfs directory for the
	// given template. The directory should contain a standard Linux
	// filesystem layout with at least /bin/sh.
	RootfsPath(templateID string) (string, error)
}

// LocalTemplateStore looks up templates as subdirectories of a base path.
//
// NOTE: Experimental
type LocalTemplateStore struct {
	BaseDir string
}

// RootfsPath returns baseDir/templateID.
func (s *LocalTemplateStore) RootfsPath(templateID string) (string, error) {
	p := filepath.Join(s.BaseDir, templateID)
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("template %q not found: %w", templateID, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("template %q is not a directory", templateID)
	}
	return p, nil
}

// CreateSandbox creates a new gVisor sandbox with the given options.
func (p *GVisorSandboxProvider) CreateSandbox(ctx context.Context, opts SandboxOptions) (Sandbox, error) {
	id, err := sandboxID()
	if err != nil {
		return nil, fmt.Errorf("generate sandbox id: %w", err)
	}

	bundleBaseDir := p.BundleBaseDir
	if bundleBaseDir == "" {
		bundleBaseDir = filepath.Join(os.TempDir(), "temporal-sandboxes")
	}
	bundleDir := filepath.Join(bundleBaseDir, id)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return nil, fmt.Errorf("create bundle dir: %w", err)
	}

	// cleanupBundleDir removes the bundle directory, logging on failure.
	cleanupBundleDir := func() {
		if err := os.RemoveAll(bundleDir); err != nil {
			log.Printf("sandbox: failed to clean up bundle dir %s: %v", bundleDir, err)
		}
	}

	// Resolve rootfs.
	rootfsPath, err := p.resolveRootfs(opts.TemplateID)
	if err != nil {
		cleanupBundleDir()
		return nil, err
	}

	// Start network proxy if allowlist is configured.
	// Default: Unix socket proxy with --network=none --host-uds=open (strong isolation).
	// Fallback: TCP proxy with --network=host (when HostNetwork is set).
	var proxy *sandboxProxy
	if len(opts.NetworkPolicy.AllowedHosts) > 0 {
		var proxyErr error
		if p.HostNetwork {
			proxy, proxyErr = newSandboxProxy(opts.NetworkPolicy.AllowedHosts)
		} else {
			proxy, proxyErr = newSandboxProxyUDS(opts.NetworkPolicy.AllowedHosts)
		}
		if proxyErr != nil {
			cleanupBundleDir()
			return nil, fmt.Errorf("start network proxy: %w", proxyErr)
		}
	}

	// Generate OCI config.
	config := p.generateOCIConfig(rootfsPath, opts, p.IgnoreCgroups, proxy)
	configBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		cleanupBundleDir()
		return nil, fmt.Errorf("marshal OCI config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "config.json"), configBytes, 0o644); err != nil {
		cleanupBundleDir()
		return nil, fmt.Errorf("write config.json: %w", err)
	}

	runscPath := p.RunscPath
	if runscPath == "" {
		runscPath = "runsc"
	}

	runscRoot := p.RunscRoot
	if runscRoot == "" {
		runscRoot = filepath.Join(os.TempDir(), "temporal-runsc")
	}
	if err := os.MkdirAll(runscRoot, 0o755); err != nil {
		cleanupBundleDir()
		return nil, fmt.Errorf("create runsc root: %w", err)
	}

	var globalFlags []string
	if p.IgnoreCgroups {
		globalFlags = append(globalFlags, "--ignore-cgroups")
	}
	if p.Disable9PGofer {
		globalFlags = append(globalFlags, "--directfs=false")
	}
	if p.HostNetwork {
		// Fallback: --network=host allows sandbox to reach TCP proxy on localhost.
		// Tools that bypass HTTP_PROXY can connect directly (weaker isolation).
		globalFlags = append(globalFlags, "--network=host")
	} else if proxy != nil {
		// UDS proxy mode: --network=none disables external networking (loopback
		// still exists for the socat bridge). --host-uds=open allows connect()
		// to the bind-mounted proxy Unix socket — the only exit path.
		globalFlags = append(globalFlags, "--network=none", "--host-uds=open")
	} else {
		// No network policy: --network=none with no UDS access.
		// Sandbox has no network at all.
		globalFlags = append(globalFlags, "--network=none")
	}
	globalFlags = append(globalFlags, p.ExtraRunscFlags...)

	sb := &gvisorSandbox{
		id:          id,
		runscPath:   runscPath,
		runscRoot:   runscRoot,
		bundleDir:   bundleDir,
		globalFlags: globalFlags,
		proxy:       proxy,
	}

	cleanup := func() {
		if proxy != nil {
			if err := proxy.Close(); err != nil {
				log.Printf("sandbox: failed to close proxy: %v", err)
			}
		}
		cleanupBundleDir()
	}

	// Create and start the sandbox.
	if err := sb.runRunsc(ctx, "create", "--bundle", bundleDir, id); err != nil {
		cleanup()
		return nil, fmt.Errorf("runsc create: %w", err)
	}
	if err := sb.runRunsc(ctx, "start", id); err != nil {
		if delErr := sb.runRunsc(context.Background(), "delete", id); delErr != nil {
			log.Printf("sandbox: failed to delete after start failure: %v", delErr)
		}
		cleanup()
		return nil, fmt.Errorf("runsc start: %w", err)
	}

	// In UDS proxy mode, start a socat bridge inside the sandbox that
	// forwards TCP 127.0.0.1:3128 → Unix socket /var/run/proxy/proxy.sock.
	// This allows standard tools (wget, curl, pip, npm, git) to use
	// HTTP_PROXY=http://127.0.0.1:3128 while the actual proxy runs on
	// the host via the bind-mounted Unix socket.
	if proxy != nil && proxy.SockDir() != "" {
		socatCmd := fmt.Sprintf("socat TCP-LISTEN:%d,fork,reuseaddr UNIX-CONNECT:/var/run/proxy/proxy.sock &", sandboxProxyPort)
		if err := sb.runRunsc(ctx, "exec", "-detach", id, "sh", "-c", socatCmd); err != nil {
			log.Printf("sandbox: socat bridge failed (network proxy may not work): %v", err)
		}
	}

	return sb, nil
}

func (p *GVisorSandboxProvider) resolveRootfs(templateID string) (string, error) {
	if templateID != "" && p.TemplateStore != nil {
		return p.TemplateStore.RootfsPath(templateID)
	}
	if p.DefaultRootfs != "" {
		return p.DefaultRootfs, nil
	}
	return "/", nil
}

// generateOCIConfig builds a minimal OCI runtime spec.
func (p *GVisorSandboxProvider) generateOCIConfig(rootfsPath string, opts SandboxOptions, ignoreCgroups bool, proxy *sandboxProxy) map[string]any {
	mounts := []map[string]any{
		{"destination": "/proc", "type": "proc", "source": "proc"},
		{"destination": "/dev", "type": "tmpfs", "source": "tmpfs", "options": []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
		{"destination": "/sys", "type": "sysfs", "source": "sysfs", "options": []string{"nosuid", "noexec", "nodev", "ro"}},
		{"destination": "/tmp", "type": "tmpfs", "source": "tmpfs", "options": []string{"nosuid", "nodev", "mode=1777", "size=1g"}},
	}

	// Bind-mount workspace as /data if provided.
	if opts.WorkspacePath != "" {
		mounts = append(mounts, map[string]any{
			"destination": "/data",
			"type":        "bind",
			"source":      opts.WorkspacePath,
			"options":     []string{"rbind", "rw"},
		})
	}

	// Bind-mount proxy socket directory if using UDS proxy.
	if proxy != nil && proxy.SockDir() != "" {
		mounts = append(mounts, map[string]any{
			"destination": "/var/run/proxy",
			"type":        "bind",
			"source":      proxy.SockDir(),
			"options":     []string{"rbind", "ro"},
		})
	}

	// Build process env.
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm",
	}
	if proxy != nil {
		var proxyURL string
		if proxy.SockDir() != "" {
			// UDS mode: socat bridge inside sandbox forwards
			// TCP 127.0.0.1:3128 → Unix socket. Standard tools
			// use the TCP address via HTTP_PROXY.
			proxyURL = fmt.Sprintf("http://127.0.0.1:%d", sandboxProxyPort)
		} else {
			// TCP mode: proxy reachable on host localhost.
			proxyURL = "http://" + proxy.Addr()
		}
		env = append(env,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
		)
	}

	// Resource limits (cgroups). Skipped when ignoreCgroups is true.
	namespaces := []map[string]string{
		{"type": "pid"},
		{"type": "ipc"},
		{"type": "uts"},
		{"type": "mount"},
	}
	// Note: we do NOT add a network namespace here. Network isolation is
	// handled by runsc flags:
	// - Default (--network=none --host-uds=open): no network stack at all.
	//   The only exit is the bind-mounted Unix socket proxy.
	// - Fallback (--network=host): shares host network, proxy on TCP localhost.
	linuxSection := map[string]any{
		"namespaces": namespaces,
	}
	if !ignoreCgroups {
		limits := opts.ResourceLimits
		linuxResources := map[string]any{}
		cpus := limits.CPUs
		if cpus <= 0 {
			cpus = 1.0
		}
		period := int64(100000)
		quota := int64(cpus * float64(period))
		linuxResources["cpu"] = map[string]any{
			"quota":  quota,
			"period": period,
		}

		memMB := limits.MemoryMB
		if memMB <= 0 {
			memMB = 512
		}
		linuxResources["memory"] = map[string]any{
			"limit": memMB * 1024 * 1024,
		}

		maxPIDs := limits.MaxPIDs
		if maxPIDs <= 0 {
			maxPIDs = 256
		}
		linuxResources["pids"] = map[string]any{
			"limit": maxPIDs,
		}

		if limits.DiskIOPS > 0 {
			linuxResources["blockIO"] = map[string]any{
				"throttleReadIOPSDevice": []map[string]any{
					{"major": 0, "minor": 0, "rate": limits.DiskIOPS},
				},
				"throttleWriteIOPSDevice": []map[string]any{
					{"major": 0, "minor": 0, "rate": limits.DiskIOPS},
				},
			}
		}
		linuxSection["resources"] = linuxResources
	}

	config := map[string]any{
		"ociVersion": "1.1.0",
		"process": map[string]any{
			"terminal": false,
			"user":     map[string]any{"uid": 0, "gid": 0},
			"args":     []string{"/bin/sleep", "infinity"},
			"env":      env,
			"cwd":      "/data",
			"rlimits": []map[string]any{
				{"type": "RLIMIT_NOFILE", "hard": uint64(1024), "soft": uint64(1024)},
			},
		},
		"root": map[string]any{
			"path":     rootfsPath,
			"readonly": true,
		},
		"mounts": mounts,
		"linux":  linuxSection,
	}
	return config
}

// gvisorSandbox is a running gVisor sandbox.
type gvisorSandbox struct {
	id          string
	runscPath   string
	runscRoot   string
	bundleDir   string
	globalFlags []string
	proxy       *sandboxProxy // nil if no network policy

	mu     sync.Mutex
	closed bool
}

// Exec runs a command inside the sandbox via runsc exec.
func (s *gvisorSandbox) Exec(ctx context.Context, cmd string, opts ExecOptions) (*ExecResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("sandbox %s is closed", s.id)
	}
	s.mu.Unlock()

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	args := []string{"exec"}

	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "/data"
	}
	args = append(args, "--cwd", workDir)

	for _, e := range opts.Env {
		args = append(args, "--env", e)
	}

	args = append(args, s.id, "/bin/sh", "-c", cmd)

	c := exec.CommandContext(ctx, s.runscPath, s.runscArgs(args...)...)
	c.WaitDelay = 3 * time.Second

	// Use temp files for stdout/stderr to avoid blocking when child
	// processes inherit pipe FDs (same issue as runRunsc).
	stdoutFile, err := os.CreateTemp("", "runsc-exec-out-*")
	if err != nil {
		return nil, fmt.Errorf("create stdout temp: %w", err)
	}
	defer os.Remove(stdoutFile.Name())
	stderrFile, err := os.CreateTemp("", "runsc-exec-err-*")
	if err != nil {
		stdoutFile.Close()
		return nil, fmt.Errorf("create stderr temp: %w", err)
	}
	defer os.Remove(stderrFile.Name())

	c.Stdout = stdoutFile
	c.Stderr = stderrFile

	runErr := c.Run()

	stdoutFile.Seek(0, 0)
	stdoutData, _ := io.ReadAll(stdoutFile)
	stdoutFile.Close()
	stderrFile.Seek(0, 0)
	stderrData, _ := io.ReadAll(stderrFile)
	stderrFile.Close()

	result := &ExecResult{
		Stdout: stdoutData,
		Stderr: stderrData,
	}

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, fmt.Errorf("exec in sandbox %s: %w", s.id, runErr)
	}
	return result, nil
}

// Close tears down the sandbox.
func (s *gvisorSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	// Force-delete the sandbox. Inside containers, runsc delete --force
	// can hang because Go's cmd.Wait() blocks on inherited FDs from
	// forked sandbox processes. We use a short timeout: if the delete
	// doesn't finish in 500ms, kill the runsc process and move on.
	// The sandbox processes themselves are already dead (SIGKILL sent
	// internally by --force); it's only the runsc CLI process that hangs.
	delCtx, delCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	delCmd := exec.CommandContext(delCtx, s.runscPath, s.runscArgs("delete", "--force", s.id)...)
	delCmd.Stdout = nil
	delCmd.Stderr = nil
	delCmd.WaitDelay = 100 * time.Millisecond
	if err := delCmd.Run(); err != nil {
		// Timeout or other error — acceptable, sandbox is already dead.
		log.Printf("sandbox: delete --force for %s: %v (non-fatal)", s.id, err)
	}
	delCancel()

	// Clean up runsc state directory entries for this sandbox.
	// The fire-and-forget delete may not clean these up in time.
	// runsc creates: {root}/{id}/ (directory) and {root}/{id}_sandbox:{id}.lock/.state (files).
	stateDir := filepath.Join(s.runscRoot, s.id)
	if err := os.RemoveAll(stateDir); err != nil {
		log.Printf("sandbox: failed to remove state dir %s: %v", stateDir, err)
	}
	prefix := s.id + "_sandbox:" + s.id
	for _, suffix := range []string{".lock", ".state"} {
		p := filepath.Join(s.runscRoot, prefix+suffix)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("sandbox: failed to remove state file %s: %v", p, err)
		}
	}

	if s.proxy != nil {
		if err := s.proxy.Close(); err != nil {
			log.Printf("sandbox: failed to close proxy for %s: %v", s.id, err)
		}
	}
	if err := os.RemoveAll(s.bundleDir); err != nil {
		log.Printf("sandbox: failed to remove bundle dir %s: %v", s.bundleDir, err)
	}
	return nil
}

// runscArgs prepends global flags (--root, --ignore-cgroups, extras) to the given args.
func (s *gvisorSandbox) runscArgs(args ...string) []string {
	result := append([]string{"--root", s.runscRoot}, s.globalFlags...)
	return append(result, args...)
}

// runRunsc executes a runsc command and returns an error if it fails.
// stdout/stderr are redirected to a temp file to avoid blocking when
// child processes (gofer, sandbox) inherit pipe file descriptors —
// Go's cmd.Run() waits for all pipe readers to close, which never
// happens if a long-lived child inherited the pipe.
func (s *gvisorSandbox) runRunsc(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, s.runscPath, s.runscArgs(args...)...)
	c.WaitDelay = 3 * time.Second // Force-close pipes if child processes keep FDs open after exit
	outFile, err := os.CreateTemp(s.bundleDir, "runsc-out-*")
	if err != nil {
		return fmt.Errorf("create temp for runsc output: %w", err)
	}
	defer os.Remove(outFile.Name())
	c.Stdout = outFile
	c.Stderr = outFile
	if runErr := c.Run(); runErr != nil {
		outFile.Seek(0, 0)
		out, _ := io.ReadAll(outFile)
		outFile.Close()
		return fmt.Errorf("runsc %s: %w: %s", strings.Join(args, " "), runErr, string(out))
	}
	outFile.Close()
	return nil
}

// runRunscOutput executes a runsc command and returns its output.
func (s *gvisorSandbox) runRunscOutput(ctx context.Context, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, s.runscPath, s.runscArgs(args...)...)
	c.WaitDelay = 3 * time.Second
	outFile, err := os.CreateTemp("", "runsc-out-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(outFile.Name())
	c.Stdout = outFile
	c.Stderr = outFile
	if runErr := c.Run(); runErr != nil {
		outFile.Seek(0, 0)
		out, _ := io.ReadAll(outFile)
		outFile.Close()
		return out, runErr
	}
	outFile.Seek(0, 0)
	out, _ := io.ReadAll(outFile)
	outFile.Close()
	return out, nil
}

// sandboxProxyPort is the TCP port the socat bridge listens on inside the
// sandbox's loopback (127.0.0.1). Standard HTTP proxy port. Since the sandbox
// runs with --network=none, this port is only reachable from inside the sandbox.
const sandboxProxyPort = 3128

func sandboxID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sb-" + hex.EncodeToString(b), nil
}
