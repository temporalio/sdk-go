package testsuite

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/log"
)

func TestWaitServerReady_respectsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()

	hostPort, err := getFreeHostPort()
	require.NoError(t, err, "get free host port")

	startTime := time.Now()
	_, err = waitServerReady(ctx, client.Options{
		HostPort:  hostPort,
		Namespace: "default",
		Logger:    log.NewNopLogger(),
	})
	require.Error(t, err, "Dial should fail")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	// The timeout is 1ms. The one-second bound allows CI scheduler jitter while
	// still detecting ignored cancellation.
	assert.Less(t, time.Since(startTime), time.Second)
}

func TestRetryFor_respectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	attempts := 0
	err := retryFor(ctx, 2, 100*time.Millisecond, func() error {
		attempts++
		return assert.AnError
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, attempts)
}

func TestMoveDownloadedCLI(t *testing.T) {
	for _, cached := range []string{"absent", "file", "directory"} {
		t.Run(cached, func(t *testing.T) {
			dir := t.TempDir()
			tempPath := filepath.Join(dir, "download")
			exePath := filepath.Join(dir, "cli")
			switch cached {
			case "file":
				require.NoError(t, os.WriteFile(exePath, []byte("winner"), 0600))
			case "directory":
				require.NoError(t, os.Mkdir(exePath, 0700))
			}
			// A missing source forces rename failure on every platform.
			err := moveDownloadedCLI(tempPath, exePath)
			if cached == "file" {
				require.NoError(t, err)
				contents, err := os.ReadFile(exePath)
				require.NoError(t, err)
				require.Equal(t, "winner", string(contents))
				return
			}
			var renameErr *os.LinkError
			require.ErrorAs(t, err, &renameErr)
			require.Equal(t, "rename", renameErr.Op)
			require.Equal(t, tempPath, renameErr.Old)
			require.Equal(t, exePath, renameErr.New)
		})
	}
}

type downloadTransport func(*http.Request) (*http.Response, error)

func (f downloadTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDownloadIfNeededTemporaryFiles(t *testing.T) {
	for _, outcome := range []string{"invalid archive", "published", "destination directory"} {
		t.Run(outcome, func(t *testing.T) {
			original := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = original })
			dir := t.TempDir()
			exePath := filepath.Join(dir, "temporal-cli-test")
			if runtime.GOOS == "windows" {
				exePath += ".exe"
			}
			var archive bytes.Buffer
			if outcome != "invalid archive" {
				writer := zip.NewWriter(&archive)
				file, err := writer.Create("cli")
				require.NoError(t, err)
				_, err = file.Write([]byte("downloaded CLI"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
			}
			http.DefaultTransport = downloadTransport(func(req *http.Request) (*http.Response, error) {
				body := archive.Bytes()
				if req.URL.Host == "temporal.download" {
					if outcome == "destination directory" {
						require.NoError(t, os.Mkdir(exePath, 0700))
					}
					body = []byte(`{"archiveUrl":"https://archive.test/cli.zip","fileToExtract":"cli"}`)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
			})
			options := &DevServerOptions{CachedDownload: CachedDownload{Version: "test", DestDir: dir}}
			path, err := downloadIfNeeded(t.Context(), options, log.NewNopLogger())
			if outcome == "published" {
				require.NoError(t, err)
				contents, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "downloaded CLI", string(contents))
				http.DefaultTransport = downloadTransport(func(*http.Request) (*http.Response, error) {
					t.Error("cache hit must not download again")
					return nil, assert.AnError
				})
				cached, err := downloadIfNeeded(t.Context(), options, log.NewNopLogger())
				require.NoError(t, err)
				require.Equal(t, path, cached)
			} else if outcome == "invalid archive" {
				require.ErrorIs(t, err, zip.ErrFormat)
			} else {
				var renameErr *os.LinkError
				require.ErrorAs(t, err, &renameErr)
				require.Equal(t, "rename", renameErr.Op)
				require.Equal(t, exePath, renameErr.New)
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, "temporal-cli-downloading-*"))
			require.NoError(t, err)
			require.Empty(t, leftovers)
		})
	}
}
