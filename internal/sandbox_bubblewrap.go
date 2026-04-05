package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// BubblewrapSandboxProvider creates sandboxes using bubblewrap (bwrap).
// It implements SandboxProvider.
//
// Bubblewrap uses Linux namespaces (mount, PID, network, IPC, UTS) to isolate
// sandboxed processes. Unlike gVisor, sandboxed code runs against the real
// kernel — isolation is at the namespace level (same as Docker/Podman).
//
// Advantages over gVisor:
//   - ~5-10ms sandbox creation (vs ~60-75ms for gVisor)
//   - ~1-2ms per-exec (vs ~10-13ms for gVisor)
//   - Full FUSE compatibility (no getcwd/fsync/readdir issues)
//   - Single binary (~80KB), no gofer/sentry/socat overhead
//
// Trade-off: namespace isolation does not protect against kernel exploits.
// For multi-tenant environments with mutually distrusting users, consider
// GVisorSandboxProvider instead.
//
// NOTE: Experimental
type BubblewrapSandboxProvider struct {
	// BwrapPath is the path to the bwrap binary. Defaults to "bwrap" (PATH lookup).
	BwrapPath string

	// DefaultRootfs is the path to the default root filesystem directory
	// used when SandboxOptions.TemplateID is empty. If empty, the host
	// rootfs is bind-mounted read-only.
	DefaultRootfs string

	// TemplateStore resolves template IDs to rootfs paths. Optional.
	TemplateStore TemplateStore

	// IgnoreCgroups disables cgroup resource limits. When true,
	// ResourceLimits in SandboxOptions are ignored.
	IgnoreCgroups bool

	// HostNetwork uses the host network namespace instead of creating an
	// isolated one. Default (false) creates a new network namespace with
	// no connectivity — only the proxy Unix socket provides egress.
	// Set to true as a fallback when network namespaces are not available.
	HostNetwork bool
}

// CreateSandbox creates a new bubblewrap sandbox.
func (p *BubblewrapSandboxProvider) CreateSandbox(ctx context.Context, opts SandboxOptions) (Sandbox, error) {
	id, err := sandboxID()
	if err != nil {
		return nil, fmt.Errorf("generate sandbox id: %w", err)
	}

	bwrapPath := p.BwrapPath
	if bwrapPath == "" {
		bwrapPath = "bwrap"
	}
	if _, err := exec.LookPath(bwrapPath); err != nil {
		return nil, fmt.Errorf("bwrap not found at %q: %w", bwrapPath, err)
	}

	rootfsPath, err := p.resolveRootfs(opts.TemplateID)
	if err != nil {
		return nil, err
	}

	// Create a temp directory for sandbox state.
	stateDir, err := os.MkdirTemp("", "temporal-bwrap-"+id+"-")
	if err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	cleanupStateDir := func() { os.RemoveAll(stateDir) }

	// Start network proxy if allowlist is configured.
	var proxy *sandboxProxy
	if len(opts.NetworkPolicy.AllowedHosts) > 0 {
		var proxyErr error
		if p.HostNetwork {
			proxy, proxyErr = newSandboxProxy(opts.NetworkPolicy.AllowedHosts)
		} else {
			proxy, proxyErr = newSandboxProxyUDS(opts.NetworkPolicy.AllowedHosts)
		}
		if proxyErr != nil {
			cleanupStateDir()
			return nil, fmt.Errorf("start network proxy: %w", proxyErr)
		}
	}

	cleanup := func() {
		if proxy != nil {
			proxy.Close()
		}
		cleanupStateDir()
	}

	// Build environment variables.
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm",
	}
	if proxy != nil {
		var proxyURL string
		if proxy.SockDir() != "" {
			proxyURL = fmt.Sprintf("http://127.0.0.1:%d", sandboxProxyPort)
		} else {
			proxyURL = "http://" + proxy.Addr()
		}
		env = append(env,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
		)
	}

	// Build bwrap args.
	args := bwrapArgs(rootfsPath, opts, proxy, p.HostNetwork, env)
	args = append(args, "--info-fd", "3")
	args = append(args, "--", "/bin/sleep", "infinity")

	// Create a pipe for bwrap --info-fd. bwrap writes {"child-pid": N}
	// to this FD after namespace setup completes.
	infoR, infoW, err := os.Pipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create info pipe: %w", err)
	}

	cmd := exec.CommandContext(ctx, bwrapPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Pass the write end of the pipe as FD 3.
	cmd.ExtraFiles = []*os.File{infoW}

	// Capture bwrap stderr to diagnose startup failures.
	bwrapStderrFile, err := os.CreateTemp("", "bwrap-stderr-*")
	if err != nil {
		infoR.Close()
		infoW.Close()
		cleanup()
		return nil, fmt.Errorf("create bwrap stderr temp: %w", err)
	}
	cmd.Stderr = bwrapStderrFile

	// Start bwrap from a locked OS thread. bwrap uses --die-with-parent
	// which sets PR_SET_PDEATHSIG(SIGKILL) on the child. This signal fires
	// when the *parent thread* exits — not the parent process. In Go, the
	// runtime multiplexes goroutines across OS threads, so the thread that
	// called cmd.Start() can be recycled, killing the bwrap child. Locking
	// the thread prevents this. The locked thread is released after
	// cmd.Wait() completes in Close().
	startCh := make(chan error, 1)
	waitCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := cmd.Start(); err != nil {
			runtime.UnlockOSThread()
			startCh <- err
			return
		}
		startCh <- nil
		// Keep this goroutine (and its OS thread) alive until bwrap exits.
		waitErr := cmd.Wait()
		waitCh <- waitErr
		runtime.UnlockOSThread()
	}()
	if err := <-startCh; err != nil {
		infoR.Close()
		infoW.Close()
		bwrapStderrFile.Close()
		os.Remove(bwrapStderrFile.Name())
		cleanup()
		return nil, fmt.Errorf("bwrap start: %w", err)
	}
	// Close write end in parent — bwrap inherited it.
	infoW.Close()

	// Read the child PID from bwrap's info-fd output.
	childPid, err := readBwrapInfo(infoR, 5*time.Second)
	infoR.Close()
	if err != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// Read bwrap stderr for diagnostics.
		bwrapStderrFile.Seek(0, 0)
		stderrData, _ := io.ReadAll(bwrapStderrFile)
		bwrapStderrFile.Close()
		os.Remove(bwrapStderrFile.Name())
		cleanup()
		if len(stderrData) > 0 {
			return nil, fmt.Errorf("read bwrap info: %w (bwrap stderr: %s)", err, string(stderrData))
		}
		return nil, fmt.Errorf("read bwrap info: %w", err)
	}

	// Wait for the sandbox mount namespace to be ready. bwrap writes
	// child-pid to info-fd before the child's mount namespace is fully
	// set up, so nsenter may fail with "No such file or directory" if
	// we enter too early.
	if err := waitForSandboxReady(childPid, 5*time.Second); err != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// Read bwrap stderr for diagnostics.
		bwrapStderrFile.Seek(0, 0)
		stderrData, _ := io.ReadAll(bwrapStderrFile)
		bwrapStderrFile.Close()
		os.Remove(bwrapStderrFile.Name())
		cleanup()
		// Also check if bwrap exited early.
		select {
		case waitErr := <-waitCh:
			return nil, fmt.Errorf("sandbox not ready (bwrap exited: %v, stderr: %s): %w", waitErr, string(stderrData), err)
		default:
			if len(stderrData) > 0 {
				return nil, fmt.Errorf("sandbox not ready (bwrap stderr: %s): %w", string(stderrData), err)
			}
			return nil, fmt.Errorf("sandbox not ready: %w", err)
		}
	}
	// Sandbox is ready — close the stderr file (no longer needed).
	bwrapStderrFile.Close()
	os.Remove(bwrapStderrFile.Name())

	// Set up cgroup resource limits if enabled.
	var cgroupID string
	if !p.IgnoreCgroups {
		cgroupDir, cgErr := createSandboxCgroup(id, opts.ResourceLimits)
		if cgErr != nil {
			log.Printf("sandbox: cgroup setup failed (resource limits not enforced): %v", cgErr)
		} else {
			cgroupID = id
			// Move the sandbox init process into the cgroup.
			if err := os.WriteFile(filepath.Join(cgroupDir, "cgroup.procs"), []byte(fmt.Sprintf("%d", childPid)), 0o644); err != nil {
				log.Printf("sandbox: failed to move pid %d into cgroup (resource limits not enforced): %v", childPid, err)
				removeSandboxCgroup(id)
				cgroupID = ""
			}
		}
	}

	// If using UDS proxy with network isolation, start socat bridge
	// inside the sandbox's network namespace.
	var socatCmd *exec.Cmd
	if proxy != nil && proxy.SockDir() != "" && !p.HostNetwork {
		socatCmd, err = startSocatBridge(childPid)
		if err != nil {
			log.Printf("sandbox: socat bridge failed (network proxy may not work): %v", err)
		}
	}

	sb := &bwrapSandbox{
		id:        id,
		stateDir:  stateDir,
		initCmd:   cmd,
		initPid:   childPid,
		proxy:     proxy,
		socatCmd:  socatCmd,
		env:       env,
		hostNet:   p.HostNetwork,
		cgroupID:  cgroupID,
	}

	return sb, nil
}

func (p *BubblewrapSandboxProvider) resolveRootfs(templateID string) (string, error) {
	if templateID != "" && p.TemplateStore != nil {
		return p.TemplateStore.RootfsPath(templateID)
	}
	if p.DefaultRootfs != "" {
		return p.DefaultRootfs, nil
	}
	return "/", nil
}

// bwrapArgs constructs bwrap flags for sandbox creation.
func bwrapArgs(rootfsPath string, opts SandboxOptions, proxy *sandboxProxy, hostNet bool, env []string) []string {
	args := []string{
		"--die-with-parent",
	}

	// Rootfs: read-only bind mount. We use --ro-bind instead of
	// --overlay-src/--tmp-overlay because overlayfs requires root even with
	// user namespaces, and --ro-bind works with bwrap 0.9+ without privileges.
	// Writable paths (/tmp, /dev/shm, /data) are mounted separately.
	args = append(args, "--ro-bind", rootfsPath, "/")

	// Essential filesystems.
	args = append(args, "--proc", "/proc")
	args = append(args, "--dev", "/dev")
	args = append(args, "--tmpfs", "/tmp")
	args = append(args, "--tmpfs", "/dev/shm")
	// Make /run writable (tmpfs) so sub-mounts like /run/proxy work.
	args = append(args, "--tmpfs", "/run")

	// Namespaces.
	args = append(args, "--unshare-pid")
	args = append(args, "--unshare-ipc")
	args = append(args, "--unshare-uts")
	if !hostNet {
		args = append(args, "--unshare-net")
	}

	// Workspace bind-mount at /data.
	if opts.WorkspacePath != "" {
		args = append(args, "--bind", opts.WorkspacePath, "/data")
	}

	// Proxy socket bind-mount at /run/proxy. /run is a tmpfs (writable)
	// so bwrap can create the mountpoint directory.
	if proxy != nil && proxy.SockDir() != "" {
		args = append(args, "--ro-bind", proxy.SockDir(), "/run/proxy")
	}

	// Working directory.
	if opts.WorkspacePath != "" {
		args = append(args, "--chdir", "/data")
	}

	// Environment.
	args = append(args, "--clearenv")
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			args = append(args, "--setenv", parts[0], parts[1])
		}
	}

	return args
}

// bwrapSandbox is a running bubblewrap sandbox.
type bwrapSandbox struct {
	id       string
	stateDir string
	initCmd  *exec.Cmd
	initPid  int
	proxy    *sandboxProxy
	socatCmd *exec.Cmd
	env      []string
	hostNet  bool
	cgroupID string // non-empty if cgroup was created

	mu     sync.Mutex
	closed bool
}

// Exec runs a command inside the sandbox via nsenter into the init
// process's namespaces.
func (s *bwrapSandbox) Exec(ctx context.Context, cmd string, opts ExecOptions) (*ExecResult, error) {
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

	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "/data"
	}


	// Build nsenter args to join the init process's namespaces.
	nsenterArgs := []string{
		"-t", fmt.Sprintf("%d", s.initPid),
		"-m", "-p", "-i", "-u",
	}
	if !s.hostNet {
		nsenterArgs = append(nsenterArgs, "-n")
	}

	// Build the inner shell command that clears env, sets vars, cd's, and execs.
	// We use /bin/sh to avoid depending on /usr/bin/env which may not exist
	// in minimal rootfs templates.
	var envSetup strings.Builder
	for _, e := range s.env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			fmt.Fprintf(&envSetup, "export %s=%s; ", parts[0], bwrapShellQuote(parts[1]))
		}
	}
	for _, e := range opts.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			fmt.Fprintf(&envSetup, "export %s=%s; ", parts[0], bwrapShellQuote(parts[1]))
		}
	}

	// Use cd with fallback so sandboxes without /data don't fail on the cd.
	innerCmd := fmt.Sprintf("%scd %s 2>/dev/null; %s", envSetup.String(), bwrapShellQuote(workDir), cmd)
	nsenterArgs = append(nsenterArgs, "--", "/bin/sh", "-c", innerCmd)

	c := exec.CommandContext(ctx, "nsenter", nsenterArgs...)

	// Use temp files for stdout/stderr (same pattern as gVisor impl).
	stdoutFile, err := os.CreateTemp("", "bwrap-exec-out-*")
	if err != nil {
		return nil, fmt.Errorf("create stdout temp: %w", err)
	}
	defer os.Remove(stdoutFile.Name())
	stderrFile, err := os.CreateTemp("", "bwrap-exec-err-*")
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
func (s *bwrapSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	// Kill the socat bridge if running.
	if s.socatCmd != nil && s.socatCmd.Process != nil {
		s.socatCmd.Process.Kill()
		s.socatCmd.Wait()
	}

	// Kill the bwrap process group and its child. The child (sleep infinity)
	// may be in a different process group, so kill it directly too.
	// Kill child first (terminates namespace), then bwrap (cleans up).
	if s.initPid > 0 {
		syscall.Kill(s.initPid, syscall.SIGKILL)
	}
	if s.initCmd.Process != nil {
		// Kill the process group. cmd.Wait() is called by the background
		// goroutine that started bwrap (to keep the OS thread alive for
		// --die-with-parent). Calling Process.Kill here is safe — it
		// just ensures the process is dead; the goroutine's Wait() will
		// return shortly after.
		syscall.Kill(-s.initCmd.Process.Pid, syscall.SIGKILL)
	}

	// Clean up cgroup (must happen after processes are killed).
	if s.cgroupID != "" {
		removeSandboxCgroup(s.cgroupID)
	}

	if s.proxy != nil {
		if err := s.proxy.Close(); err != nil {
			log.Printf("sandbox: failed to close proxy for %s: %v", s.id, err)
		}
	}

	if err := os.RemoveAll(s.stateDir); err != nil {
		log.Printf("sandbox: failed to remove state dir %s: %v", s.stateDir, err)
	}
	return nil
}

// readBwrapInfo reads the JSON info from bwrap's --info-fd pipe and returns
// the child PID. bwrap writes {"child-pid": N} after namespace setup.
func readBwrapInfo(r *os.File, timeout time.Duration) (int, error) {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			return 0, fmt.Errorf("read info-fd: %w", res.err)
		}
		var info struct {
			ChildPid int `json:"child-pid"`
		}
		if err := json.Unmarshal(res.data, &info); err != nil {
			return 0, fmt.Errorf("parse bwrap info %q: %w", string(res.data), err)
		}
		if info.ChildPid <= 0 {
			return 0, fmt.Errorf("invalid child-pid %d from bwrap info", info.ChildPid)
		}
		return info.ChildPid, nil
	case <-time.After(timeout):
		return 0, fmt.Errorf("timeout waiting for bwrap info-fd after %v", timeout)
	}
}

// waitForSandboxReady polls until nsenter can successfully enter the
// sandbox's mount namespace and find /bin/sh. This handles the race between
// bwrap writing child-pid and the mount namespace being fully set up.
func waitForSandboxReady(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pidStr := fmt.Sprintf("%d", pid)
	var lastErr error
	for time.Now().Before(deadline) {
		cmd := exec.Command("nsenter", "-t", pidStr, "-m", "--", "/bin/sh", "-c", "true")
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%w: %s", err, string(out))
		time.Sleep(5 * time.Millisecond)
	}
	if _, statErr := os.Stat(fmt.Sprintf("/proc/%d", pid)); statErr != nil {
		return fmt.Errorf("sandbox pid %d does not exist (died?): last nsenter error: %v", pid, lastErr)
	}
	return fmt.Errorf("sandbox pid %d mount namespace not ready after %v (last error: %v)", pid, timeout, lastErr)
}

// startSocatBridge starts a socat TCP-to-UDS bridge inside the sandbox's
// network namespace via nsenter.
func startSocatBridge(pid int) (*exec.Cmd, error) {
	cmd := exec.Command("nsenter",
		"-t", fmt.Sprintf("%d", pid),
		"-n", // network namespace only
		"--", "socat",
		fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr", sandboxProxyPort),
		"UNIX-CONNECT:/run/proxy/proxy.sock",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start socat bridge: %w", err)
	}
	return cmd, nil
}

// bwrapShellQuote wraps a string in single quotes for sh -c.
func bwrapShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// createSandboxCgroup creates a cgroup v2 directory for the sandbox and writes
// resource limits. Returns the cgroup directory path.
func createSandboxCgroup(id string, limits SandboxResourceLimits) (string, error) {
	base := filepath.Join("/sys/fs/cgroup", "temporal-sandbox")

	// Ensure the parent cgroup exists and has controllers delegated.
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("create cgroup parent dir: %w", err)
	}
	// Enable controllers on the parent so child cgroups can use them.
	// This may fail if the parent has processes (cgroup v2 "no internal
	// processes" rule) — that's fine, it means controllers were already
	// enabled at a higher level.
	os.WriteFile(filepath.Join(base, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o644)

	dir := filepath.Join(base, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cgroup dir: %w", err)
	}

	// CPU limit.
	cpus := limits.CPUs
	if cpus <= 0 {
		cpus = 1.0
	}
	period := int64(100000)
	quota := int64(cpus * float64(period))
	if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(fmt.Sprintf("%d %d", quota, period)), 0o644); err != nil {
		os.Remove(dir)
		return "", fmt.Errorf("write cpu.max: %w", err)
	}

	// Memory limit.
	memMB := limits.MemoryMB
	if memMB <= 0 {
		memMB = 512
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.max"), []byte(fmt.Sprintf("%d", memMB*1024*1024)), 0o644); err != nil {
		os.Remove(dir)
		return "", fmt.Errorf("write memory.max: %w", err)
	}

	// PID limit.
	maxPIDs := limits.MaxPIDs
	if maxPIDs <= 0 {
		maxPIDs = 256
	}
	if err := os.WriteFile(filepath.Join(dir, "pids.max"), []byte(fmt.Sprintf("%d", maxPIDs)), 0o644); err != nil {
		os.Remove(dir)
		return "", fmt.Errorf("write pids.max: %w", err)
	}

	return dir, nil
}

// removeSandboxCgroup removes the cgroup directory for a sandbox.
func removeSandboxCgroup(id string) {
	dir := filepath.Join("/sys/fs/cgroup", "temporal-sandbox", id)
	os.Remove(dir) // rmdir — only works if empty (all processes exited)
}
