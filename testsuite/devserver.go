// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

// Package testsuite contains unit testing framework for Temporal workflows and activities and a helper to download and
// start a dev server.
package testsuite

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
)

// Cached download of the dev server.
type CachedDownload struct {
	// Which version to download, by default the latest version compatible with the SDK will be downloaded.
	// Acceptable values are specific release versions (e.g v0.3.0), "default", and "latest".
	Version string
	// Destination directory or the user temp directory if unset.
	DestDir string
}

// Where to find an executable. Can be a path or cached download.
type EphemeralExe struct {
	// Existing path on the filesystem for the executable.
	ExistingPath string
	// Download the executable if not already there.
	CachedDownload CachedDownload
}

// Configuration for the dev server.
type DevServerOptions struct {
	// Path to executable or download info - defaults to cached download.
	Exe EphemeralExe
	// Client options used to create a client for the dev server.
	// The provided Namespace or the "default" namespace is automatically registered on startup.
	// If HostPort is provided, the host and port will be used to bind the server, otherwise the server will bind to
	// localhost and obtain a free port.
	ClientOptions *client.Options
	// SQLite DB filename if persisting or non-persistent if none.
	DBFilename string
	// Whether to enable the UI.
	EnableUI bool
	// Log format - defaults to "pretty"
	LogFormat string
	// Log level - defaults to "warn"
	LogLevel string
	// Additional arguments to the dev server.
	ExtraArgs []string
}

type DevServer interface {
	// Stop the running server and wait for shutdown to complete. Error is propagated from server shutdown.
	Stop() error
	// Get a connected client, configured to work with the dev server.
	Client() client.Client
}

// Temporal CLI based DevServer
type devServer struct {
	cmd    *exec.Cmd
	client client.Client
}

// StartDevServer starts a Temporal CLI dev server process. This may download the server if not already downloaded.
func StartDevServer(ctx context.Context, options *DevServerOptions) (DevServer, error) {
	// Accept nil because all options are "optional".
	if options == nil {
		options = &DevServerOptions{}
	}
	clientOptions := options.clientOptionsOrDefault()

	exePath, err := downloadIfNeeded(options, clientOptions.Logger)
	if err != nil {
		return nil, err
	}

	if clientOptions.HostPort == "" {
		// Make sure this is done after downloading to reduce the chance (however slim) that the free port would be used
		// up by the time the download completes.
		clientOptions.HostPort, err = getFreeHostPort()
		if err != nil {
			return nil, err
		}
	}
	host, port, err := net.SplitHostPort(clientOptions.HostPort)
	if err != nil {
		return nil, fmt.Errorf("invalid HostPort: %w", err)
	}

	args := []string{
		"server",
		"start-dev",
		"--ip", host, "--port", port,
		"--namespace", clientOptions.Namespace,
		"--dynamic-config-value", "frontend.enableServerVersionCheck=false",
		"--dynamic-config-value", "system.enableEagerWorkflowStart=true",
		"--dynamic-config-value", "system.enableEagerWorkflowStart=true",
	}
	if options.LogLevel != "" {
		args = append(args, "--log-level", options.LogLevel)
	}
	if options.LogFormat != "" {
		args = append(args, "--log-format", options.LogFormat)
	}
	cmd := exec.Command(exePath, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	clientOptions.Logger.Info("Starting DevServer", "ExePath", exePath, "Args", args)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed starting: %w", err)
	}

	returnedClient, err := waitServerReady(ctx, clientOptions)
	clientOptions.Logger.Info("DevServer ready")
	if err != nil {
		return nil, err
	}
	return &devServer{client: returnedClient, cmd: cmd}, nil
}

func downloadIfNeeded(options *DevServerOptions, logger log.Logger) (string, error) {
	if options.Exe.ExistingPath != "" {
		return options.Exe.ExistingPath, nil
	}
	version := options.Exe.CachedDownload.Version
	if version == "" {
		version = "default"
	}
	destDir := options.Exe.CachedDownload.DestDir
	if destDir == "" {
		destDir = os.TempDir()
	}
	var exePath string
	// Build path based on version and check if already present
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

	// Build info URL
	platform := runtime.GOOS
	if platform != "windows" && platform != "darwin" && platform != "linux" {
		return "", fmt.Errorf("unsupported platform %v", platform)
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported architecture %v", arch)
	}
	infoURL := fmt.Sprintf("https://temporal.download/cli/%v?platform=%v&arch=%v&sdk-name=sdk-go&sdk-version=%v", version, platform, arch, internal.SDKVersion)

	// Get info
	info := struct {
		ArchiveURL    string `json:"archiveUrl"`
		FileToExtract string `json:"fileToExtract"`
	}{}
	resp, err := http.Get(infoURL)
	if err != nil {
		return "", fmt.Errorf("failed fetching info: %w", err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("failed fetching info body: %w", err)
	} else if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed fetching info, status: %v, body: %s", resp.Status, b)
	} else if err = json.Unmarshal(b, &info); err != nil {
		return "", fmt.Errorf("failed unmarshaling info: %w", err)
	}

	// Download and extract
	logger.Info("Downloading temporal CLI", "Url", info.ArchiveURL, "ExePath", exePath)
	resp, err = http.Get(info.ArchiveURL)
	if err != nil {
		return "", fmt.Errorf("failed downloading: %w", err)
	}
	defer resp.Body.Close()
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
	if strings.HasSuffix(info.ArchiveURL, ".tar.gz") {
		err = extractTarball(resp.Body, info.FileToExtract, f)
	} else if strings.HasSuffix(info.ArchiveURL, ".zip") {
		err = extractZip(resp.Body, info.FileToExtract, f)
	} else {
		err = fmt.Errorf("unrecognized file extension on %v", info.ArchiveURL)
	}
	f.Close()
	if err != nil {
		return "", err
	}
	// Chmod it if not Windows
	if runtime.GOOS != "windows" {
		if err := os.Chmod(f.Name(), 0755); err != nil {
			return "", fmt.Errorf("failed chmod'ing file: %w", err)
		}
	}
	if err = os.Rename(f.Name(), exePath); err != nil {
		return "", fmt.Errorf("failed moving file: %w", err)
	}
	return exePath, nil
}

func (opts *DevServerOptions) clientOptionsOrDefault() client.Options {
	var out client.Options
	if opts.ClientOptions != nil {
		// Shallow copy the client options since we intend to overwrite some fields.
		out = *opts.ClientOptions
	} else {
		out = client.Options{}
	}
	if out.Logger == nil {
		out.Logger = ilog.NewDefaultLogger()
	}
	if out.Namespace == "" {
		out.Namespace = "default"
	}
	return out
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

// waitServerReady repeatedly attempts to dial the server with given options until it is ready or it is time to give up.
// Returns a connected client created using the provided options.
func waitServerReady(ctx context.Context, options client.Options) (client.Client, error) {
	var returnedClient client.Client
	lastErr := retryFor(600, 100*time.Millisecond, func() error {
		var err error
		returnedClient, err = client.Dial(options)
		return err
	})
	if lastErr != nil {
		return nil, fmt.Errorf("failed connecting after timeout, last error: %w", lastErr)
	}
	return returnedClient, lastErr
}

// retryFor retries some function until it returns nil or runs out of attempts. Wait interval between attempts.
func retryFor(maxAttempts int, interval time.Duration, cond func() error) error {
	if maxAttempts < 1 {
		// this is used internally, okay to panic
		panic("maxAttempts should be at least 1")
	}
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if curE := cond(); curE == nil {
			return nil
		} else {
			lastErr = curE
		}
		time.Sleep(interval)
	}
	return lastErr
}

func (s *devServer) Stop() error {
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	return s.cmd.Wait()
}

func (s *devServer) Client() client.Client {
	return s.client
}
