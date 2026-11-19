package testsuite

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMoveDownloadedCLIRunningExecutable(t *testing.T) {
	if os.Getenv("TEMPORAL_CLI_CACHE_TEST_CHILD") == "1" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "cached.exe")
	tempPath := filepath.Join(dir, "download.exe")
	self, err := os.Executable()
	require.NoError(t, err)
	contents, err := os.ReadFile(self)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(exePath, contents, 0700))
	// Keep the child alive through cleanup, when t.Context is already canceled.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exePath, "-test.run=^TestMoveDownloadedCLIRunningExecutable$")
	cmd.Env = append(os.Environ(), "TEMPORAL_CLI_CACHE_TEST_CHILD=1")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		require.NoError(t, stdin.Close())
		require.NoError(t, cmd.Wait())
	})
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready", strings.TrimSpace(ready))
	require.NoError(t, os.WriteFile(tempPath, []byte("other download"), 0700))
	require.Error(t, os.Rename(tempPath, exePath))
	require.NoError(t, moveDownloadedCLI(tempPath, exePath))
	actual, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.Equal(t, contents, actual)
}
