package temporalcli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/log"
)

// Archive identifies the release artifact selected by temporal.download.
type Archive struct {
	ArchiveURL    string `json:"archiveUrl"`
	FileToExtract string `json:"fileToExtract"`
}

// Resolve returns the CLI archive for this SDK version and platform.
func Resolve(ctx context.Context, version string, logger log.Logger) (Archive, error) {
	platform := runtime.GOOS
	if platform != "windows" && platform != "darwin" && platform != "linux" {
		return Archive{}, fmt.Errorf("unsupported platform %v", platform)
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return Archive{}, fmt.Errorf("unsupported architecture %v", arch)
	}
	infoURL := fmt.Sprintf("https://temporal.download/cli/%v?platform=%v&arch=%v&sdk-name=sdk-go&sdk-version=%v", url.QueryEscape(version), platform, arch, internal.SDKVersion)

	req, err := http.NewRequestWithContext(ctx, "GET", infoURL, nil)
	if err != nil {
		return Archive{}, fmt.Errorf("failed preparing request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return Archive{}, fmt.Errorf("failed fetching info: %w", err)
	}
	b, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		logger.Warn("Failed to close response body: %v", closeErr)
	}
	if err != nil {
		return Archive{}, fmt.Errorf("failed fetching info body: %w", err)
	}
	if resp.StatusCode != 200 {
		return Archive{}, fmt.Errorf("failed fetching info, status: %v, body: %s", resp.Status, b)
	}
	var info Archive
	if err := json.Unmarshal(b, &info); err != nil {
		return Archive{}, fmt.Errorf("failed unmarshaling info: %w", err)
	}
	if info.ArchiveURL == "" || info.FileToExtract == "" {
		return Archive{}, fmt.Errorf("incomplete Temporal CLI archive metadata for %q", version)
	}
	return info, nil
}

// Download returns the cached CLI path, downloading it on a cache miss.
func Download(ctx context.Context, version, destDir string, logger log.Logger) (string, error) {
	return download(ctx, version, destDir, Archive{}, logger)
}

// DownloadDefault uses the resolved archive on a cache miss without resolving the default again.
func (info Archive) DownloadDefault(ctx context.Context, destDir string, logger log.Logger) (string, error) {
	if info.ArchiveURL == "" || info.FileToExtract == "" {
		return "", fmt.Errorf("resolved default Temporal CLI archive metadata is incomplete")
	}
	return download(ctx, "default", destDir, info, logger)
}

func download(ctx context.Context, version, destDir string, info Archive, logger log.Logger) (string, error) {
	if version == "" {
		version = "default"
	}
	if destDir == "" {
		destDir = os.TempDir()
	}
	var exePath string
	if version == "default" {
		exePath = filepath.Join(destDir, "temporal-cli-go-sdk-"+internal.SDKVersion)
	} else {
		exePath = filepath.Join(destDir, "temporal-cli-"+version)
	}
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}
	if _, err := os.Stat(exePath); err == nil {
		return exePath, nil
	}
	if info.ArchiveURL == "" {
		var err error
		info, err = Resolve(ctx, version, logger)
		if err != nil {
			return "", err
		}
	}

	// Download and extract
	logger.Info("Downloading temporal CLI", "Url", info.ArchiveURL, "ExePath", exePath)
	req, err := http.NewRequestWithContext(ctx, "GET", info.ArchiveURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed preparing request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", fmt.Errorf("failed downloading: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Warn("Failed to close response body: %v", closeErr)
		}
	}()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed downloading, status: %v", resp.Status)
	}
	// We want to download to a temporary file then rename. A better system-wide
	// atomic downloader would use a common temp file and check whether it exists
	// and wait on it, but doing multiple downloads in racy situations is
	// good/simple enough for now.
	// Note that we don't use os.TempDir here, instead we use the user provided destination directory which is
	// guaranteed to make the rename atomic.
	f, err := os.CreateTemp(destDir, "temporal-cli-downloading-")
	if err != nil {
		return "", fmt.Errorf("failed creating temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(f.Name()); err != nil && !os.IsNotExist(err) {
			logger.Warn("Failed to remove temporary CLI download", "Error", err)
		}
	}()
	if strings.HasSuffix(info.ArchiveURL, ".tar.gz") {
		err = extractTarball(resp.Body, info.FileToExtract, f)
	} else if strings.HasSuffix(info.ArchiveURL, ".zip") {
		err = extractZip(resp.Body, info.FileToExtract, f)
	} else {
		err = fmt.Errorf("unrecognized file extension on %v", info.ArchiveURL)
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	} else if closeErr != nil {
		return "", fmt.Errorf("failed to close temp file: %w", closeErr)
	}
	// Chmod it if not Windows
	if runtime.GOOS != "windows" {
		if err := os.Chmod(f.Name(), 0755); err != nil {
			return "", fmt.Errorf("failed chmod'ing file: %w", err)
		}
	}
	if err := moveDownloadedCLI(f.Name(), exePath); err != nil {
		return "", err
	}
	return exePath, nil
}

func moveDownloadedCLI(tempPath, exePath string) error {
	if renameErr := os.Rename(tempPath, exePath); renameErr != nil {
		// Another process may have populated and started the cached executable on Windows.
		info, statErr := os.Stat(exePath)
		if statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("failed moving file: %w", renameErr)
		}
	}
	return nil
}

func extractTarball(r io.Reader, toExtract string, w io.Writer) error {
	r, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tarRead := tar.NewReader(r)
	for {
		h, err := tarRead.Next()
		if err != nil {
			// This can be EOF which means we never found our file
			return err
		} else if h.Name == toExtract {
			_, err = io.Copy(w, tarRead)
			return err
		}
	}
}

func extractZip(r io.Reader, toExtract string, w io.Writer) error {
	// Instead of using a third party zip streamer, and since Go stdlib doesn't
	// support streaming read, we'll just put the entire archive in memory for now
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	zipRead, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return err
	}
	for _, file := range zipRead.File {
		if file.Name == toExtract {
			r, err := file.Open()
			if err != nil {
				return err
			}
			_, err = io.Copy(w, r)
			return err
		}
	}
	return fmt.Errorf("could not find file in zip archive")
}
