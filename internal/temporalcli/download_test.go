package temporalcli

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/log"
)

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

func TestDownloadDefaultUsesResolvedArchive(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	const archiveURL = "https://archive.test/resolved-cli.zip"
	var requests []string
	http.DefaultTransport = downloadTransport(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		if req.URL.Host == "temporal.download" {
			require.Equal(t, "/cli/default", req.URL.Path)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"archiveUrl":"` + archiveURL + `","fileToExtract":"cli"}`)),
			}, nil
		}
		return &http.Response{
			Status:     "503 Service Unavailable",
			StatusCode: http.StatusServiceUnavailable,
			Body:       http.NoBody,
		}, nil
	})

	logger := log.NewNopLogger()
	archive, err := Resolve(t.Context(), "default", logger)
	require.NoError(t, err)
	_, err = archive.DownloadDefault(t.Context(), t.TempDir(), logger)
	require.ErrorContains(t, err, "failed downloading, status: 503")
	require.Len(t, requests, 2)
	require.Equal(t, archiveURL, requests[1])
}

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
			path, err := Download(t.Context(), "test", dir, log.NewNopLogger())
			if outcome == "published" {
				require.NoError(t, err)
				contents, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "downloaded CLI", string(contents))
				http.DefaultTransport = downloadTransport(func(*http.Request) (*http.Response, error) {
					t.Error("cache hit must not download again")
					return nil, assert.AnError
				})
				cached, err := Download(t.Context(), "test", dir, log.NewNopLogger())
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
