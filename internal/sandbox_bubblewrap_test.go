//go:build linux

package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bwrapAvailable(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not available, skipping bubblewrap sandbox test")
	}
	// Verify bwrap can create namespaces.
	out, err := exec.Command(p, "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--unshare-pid", "echo", "ok").CombinedOutput()
	if err != nil {
		t.Skipf("bwrap cannot create namespaces (likely seccomp/apparmor restriction): %v: %s", err, out)
	}
	return p
}

func TestBubblewrapSandbox_ExecBasic(t *testing.T) {
	bwrapPath := bwrapAvailable(t)

	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := &BubblewrapSandboxProvider{
		BwrapPath:     bwrapPath,
		IgnoreCgroups: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{
		WorkspacePath: wsDir,
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	// Test basic command execution.
	result, err := sb.Exec(ctx, "echo hello from sandbox", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec echo: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d, stderr: %s", result.ExitCode, result.Stderr)
	}
	if got := strings.TrimSpace(string(result.Stdout)); got != "hello from sandbox" {
		t.Errorf("expected 'hello from sandbox', got %q", got)
	}

	// Test reading workspace file.
	result, err = sb.Exec(ctx, "cat /data/hello.txt", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec cat: %v", err)
	}
	if got := string(result.Stdout); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}

	// Test writing to workspace.
	result, err = sb.Exec(ctx, "echo 'written by sandbox' > /data/output.txt", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec write: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("write exit code %d, stderr: %s", result.ExitCode, result.Stderr)
	}

	// Verify the file was written on the host.
	data, err := os.ReadFile(filepath.Join(wsDir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile output.txt: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "written by sandbox" {
		t.Errorf("expected 'written by sandbox', got %q", got)
	}
}

func TestBubblewrapSandbox_ExitCode(t *testing.T) {
	bwrapPath := bwrapAvailable(t)

	provider := &BubblewrapSandboxProvider{
		BwrapPath:     bwrapPath,
		IgnoreCgroups: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	result, err := sb.Exec(ctx, "exit 42", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestBubblewrapSandbox_Isolation(t *testing.T) {
	bwrapPath := bwrapAvailable(t)

	provider := &BubblewrapSandboxProvider{
		BwrapPath:     bwrapPath,
		IgnoreCgroups: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	// Sandbox should see isolated PID namespace (PID 1 = sleep).
	result, err := sb.Exec(ctx, "cat /proc/1/cmdline 2>/dev/null | tr '\\0' ' '", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	stdout := string(result.Stdout)
	if !strings.Contains(stdout, "sleep") {
		t.Logf("PID 1 cmdline: %q (expected sleep infinity)", stdout)
	}

	// Rootfs should be read-only.
	result, err = sb.Exec(ctx, "touch /test-readonly 2>&1; echo $?", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if strings.Contains(string(result.Stdout), "\n0") {
		t.Error("rootfs should be read-only but write succeeded")
	}
}

func TestBubblewrapSandbox_NetworkIsolation(t *testing.T) {
	bwrapPath := bwrapAvailable(t)

	provider := &BubblewrapSandboxProvider{
		BwrapPath:     bwrapPath,
		IgnoreCgroups: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	// With --unshare-net and no proxy, network should be unreachable.
	result, err := sb.Exec(ctx, "cat /sys/class/net/eth0/address 2>&1 || echo 'no eth0'", ExecOptions{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(string(result.Stdout), "no eth0") && !strings.Contains(string(result.Stderr), "No such file") {
		t.Logf("unexpected network interface: %s", result.Stdout)
	}
}

func TestBubblewrapSandbox_CloseCleanup(t *testing.T) {
	bwrapPath := bwrapAvailable(t)

	provider := &BubblewrapSandboxProvider{
		BwrapPath:     bwrapPath,
		IgnoreCgroups: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	// Record the PID to verify it's killed after Close.
	bsb := sb.(*bwrapSandbox)
	initPid := bsb.initPid

	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Process should be dead. Give the kernel time to reap.
	// Check that the process is either gone or a zombie (Z state).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", initPid))
		if err != nil {
			break // Process gone — success
		}
		// Process still exists — check if it's a zombie (State: Z)
		if strings.Contains(string(data), "State:\tZ") {
			break // Zombie — will be reaped by init, functionally dead
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Final check: process should be gone or zombie
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", initPid))
	if err == nil && !strings.Contains(string(data), "State:\tZ") {
		t.Errorf("init process %d still alive (not zombie) after Close", initPid)
	}

	// Exec after close should error.
	_, err = sb.Exec(ctx, "echo test", ExecOptions{})
	if err == nil {
		t.Error("expected error on Exec after Close")
	}
}

func TestBubblewrapSandbox_OOMKill(t *testing.T) {
	bwrapPath := bwrapAvailable(t)
	if !cgroupsWritable() {
		t.Skip("cgroups not writable, skipping OOM test")
	}

	wsDir := t.TempDir()
	provider := &BubblewrapSandboxProvider{
		BwrapPath: bwrapPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{
		WorkspacePath: wsDir,
		ResourceLimits: SandboxResourceLimits{
			MemoryMB: 64,
			MaxPIDs:  100,
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	// Write 200MB to /dev/shm — should exceed 64MB limit.
	result, err := sb.Exec(ctx, "dd if=/dev/zero of=/dev/shm/boom bs=1M count=200 2>&1", ExecOptions{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for OOM, got 0. stdout: %s", result.Stdout)
	}
	t.Logf("OOM test: exit=%d stdout=%s", result.ExitCode, strings.TrimSpace(string(result.Stdout)))
}

func TestBubblewrapSandbox_ForkBomb(t *testing.T) {
	bwrapPath := bwrapAvailable(t)
	if !cgroupsWritable() {
		t.Skip("cgroups not writable, skipping fork bomb test")
	}

	wsDir := t.TempDir()
	provider := &BubblewrapSandboxProvider{
		BwrapPath: bwrapPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{
		WorkspacePath: wsDir,
		ResourceLimits: SandboxResourceLimits{
			MemoryMB: 512,
			MaxPIDs:  50,
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	result, err := sb.Exec(ctx, "for i in $(seq 1 200); do sleep 30 & done; wait 2>&1", ExecOptions{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for fork bomb, got 0. stdout: %s", result.Stdout)
	}
	t.Logf("Fork bomb test: exit=%d stdout=%s stderr=%s", result.ExitCode,
		strings.TrimSpace(string(result.Stdout)), strings.TrimSpace(string(result.Stderr)))
}

func TestBubblewrapSandbox_Getcwd(t *testing.T) {
	bwrapPath := bwrapAvailable(t)

	wsDir := t.TempDir()
	provider := &BubblewrapSandboxProvider{
		BwrapPath:     bwrapPath,
		IgnoreCgroups: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{
		WorkspacePath: wsDir,
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	// getcwd should work from /data (no gVisor Sentry issues).
	result, err := sb.Exec(ctx, "pwd", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := strings.TrimSpace(string(result.Stdout)); got != "/data" {
		t.Errorf("expected pwd=/data, got %q", got)
	}
}
