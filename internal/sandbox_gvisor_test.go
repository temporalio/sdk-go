//go:build linux

package internal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func runscAvailable(t *testing.T) string {
	t.Helper()
	// Check common locations.
	for _, p := range []string{"/tmp/runsc", "/usr/local/bin/runsc"} {
		if _, err := os.Stat(p); err == nil {
			// Verify it actually works (not just present but broken).
			out, err := exec.Command(p, "--version").CombinedOutput()
			if err != nil {
				t.Skipf("runsc at %s exists but fails: %v: %s", p, err, out)
			}
			return p
		}
	}
	// Check PATH.
	p, err := exec.LookPath("runsc")
	if err != nil {
		t.Skip("runsc not available, skipping gVisor sandbox test")
	}
	return p
}

func canCreateSandbox(t *testing.T) bool {
	t.Helper()
	// gVisor needs to fork/exec and create namespaces. Check if we have
	// enough privileges by attempting unshare(CLONE_NEWPID).
	err := unix.Unshare(unix.CLONE_NEWPID)
	if err != nil {
		t.Skipf("insufficient privileges for gVisor sandbox (unshare: %v)", err)
		return false
	}
	return true
}

func cgroupsWritable() bool {
	return unix.Access("/sys/fs/cgroup/cgroup.subtree_control", unix.W_OK) == nil
}

func TestGVisorSandbox_ExecBasic(t *testing.T) {
	runscPath := runscAvailable(t)
	canCreateSandbox(t)

	// Create a temp workspace directory.
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := &GVisorSandboxProvider{
		RunscPath:     runscPath,
		IgnoreCgroups: !cgroupsWritable(),
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

func TestGVisorSandbox_ExitCode(t *testing.T) {
	runscPath := runscAvailable(t)
	canCreateSandbox(t)

	provider := &GVisorSandboxProvider{
		RunscPath:     runscPath,
		IgnoreCgroups: !cgroupsWritable(),
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

func TestGVisorSandbox_Isolation(t *testing.T) {
	runscPath := runscAvailable(t)
	canCreateSandbox(t)

	provider := &GVisorSandboxProvider{
		RunscPath:     runscPath,
		IgnoreCgroups: !cgroupsWritable(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	defer sb.Close()

	// Sandbox should not be able to see the host filesystem.
	result, err := sb.Exec(ctx, "ls /Users 2>&1 || true", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if strings.Contains(string(result.Stdout), "yimin") {
		t.Error("sandbox can see host /Users directory — isolation failure")
	}

	// Sandbox should see isolated PID namespace.
	result, err = sb.Exec(ctx, "cat /proc/1/cmdline 2>/dev/null | tr '\\0' ' '", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	stdout := string(result.Stdout)
	if !strings.Contains(stdout, "sleep") {
		t.Logf("PID 1 cmdline: %q (expected sleep infinity)", stdout)
	}
}

func TestGVisorSandbox_CloseCleanup(t *testing.T) {
	runscPath := runscAvailable(t)
	canCreateSandbox(t)

	bundleDir := t.TempDir()
	provider := &GVisorSandboxProvider{
		RunscPath:     runscPath,
		BundleBaseDir: bundleDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb, err := provider.CreateSandbox(ctx, SandboxOptions{})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	// Close should clean up bundle dir.
	if err := sb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Bundle base dir should exist but sandbox subdir should be gone.
	entries, _ := os.ReadDir(bundleDir)
	if len(entries) != 0 {
		t.Errorf("expected empty bundle dir after close, got %d entries", len(entries))
	}

	// Exec after close should error.
	_, err = sb.Exec(ctx, "echo test", ExecOptions{})
	if err == nil {
		t.Error("expected error on Exec after Close")
	}
}

func TestGVisorSandbox_OOMKill(t *testing.T) {
	runscPath := runscAvailable(t)
	canCreateSandbox(t)
	if !cgroupsWritable() {
		t.Skip("cgroups not writable, skipping OOM test")
	}

	wsDir := t.TempDir()
	provider := &GVisorSandboxProvider{
		RunscPath: runscPath,
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

	// Write 200MB to /dev/shm (RAM-backed tmpfs) — should exceed 64MB limit.
	result, err := sb.Exec(ctx, "dd if=/dev/zero of=/dev/shm/boom bs=1M count=200 2>&1", ExecOptions{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	// Sandbox should be OOM-killed: exit code 137 (SIGKILL) or 128+signal.
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for OOM, got 0. stdout: %s", result.Stdout)
	}
	t.Logf("OOM test: exit=%d stdout=%s", result.ExitCode, strings.TrimSpace(string(result.Stdout)))
}

func TestGVisorSandbox_ForkBomb(t *testing.T) {
	runscPath := runscAvailable(t)
	canCreateSandbox(t)
	if !cgroupsWritable() {
		t.Skip("cgroups not writable, skipping fork bomb test")
	}

	wsDir := t.TempDir()
	provider := &GVisorSandboxProvider{
		RunscPath: runscPath,
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

	// Fork 200 concurrent processes — should exceed 50 PID limit.
	result, err := sb.Exec(ctx, "for i in $(seq 1 200); do sleep 30 & done; wait 2>&1", ExecOptions{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	// Sandbox should be killed by PID limit.
	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for fork bomb, got 0. stdout: %s", result.Stdout)
	}
	t.Logf("Fork bomb test: exit=%d stdout=%s stderr=%s", result.ExitCode,
		strings.TrimSpace(string(result.Stdout)), strings.TrimSpace(string(result.Stderr)))
}
