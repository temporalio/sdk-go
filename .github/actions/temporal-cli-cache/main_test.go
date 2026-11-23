package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/temporalcli"
)

func TestPrefetchRejectsUnusableCLI(t *testing.T) {
	t.Chdir(t.TempDir())
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("CLI_DEFAULT_ARCHIVE_INFO", "{}")

	version := temporalcli.IntegrationServerVersion
	name := "temporal-cli-" + version
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	require.NoError(t, os.MkdirAll(cacheDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, name), []byte("invalid executable"), 0755))

	require.ErrorContains(t, prefetch([]string{version}), "checking Temporal CLI "+version)
	require.NoFileExists(t, filepath.Join(tempDir, name))
}
