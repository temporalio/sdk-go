package testsuite

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
)

const (
	cliDownloadAttempts = 3
	cliRequestTimeout   = 2 * time.Minute
)

// CachedDownload is the cached download configuration for the dev server binary.
type CachedDownload struct {
	// Which version to download, by default the latest version compatible with the SDK will be downloaded.
	// Acceptable values are specific release versions (e.g v0.3.0), "default", and "latest".
	Version string
	// Destination directory or the user temp directory if unset.
	DestDir string
}

// DevServerOptions configures the dev server process.
type DevServerOptions struct {
	// Existing path on the filesystem for the executable.
	ExistingPath string
	// Download the executable if not already there.
	CachedDownload CachedDownload
	// Client options used to create a client for the dev server.
	// The provided Namespace or the "default" namespace is automatically registered on startup.
	// If HostPort is provided, the host and port will be used to bind the server, otherwise the server will bind to
	// localhost and obtain a free port.
	ClientOptions *client.Options
	// SQLite DB filename if persisting or non-persistent if none.
	DBFilename string
	// Whether to enable the UI.
	EnableUI bool
	// Override UI port if EnableUI is true.
	// If not provided, a free port will be used.
	UIPort string
	// Log format - defaults to "pretty".
	LogFormat string
	// Log level - defaults to "warn".
	LogLevel string
	// Search Attributes to register with the dev server.
	SearchAttributes temporal.SearchAttributes
	// Additional arguments to the dev server.
	ExtraArgs []string
	// Where to redirect stdout and stderr, if nil they will be redirected to the current process.
	Stdout io.Writer
	Stderr io.Writer
}

// DevServer is a Temporal CLI-based dev server process.
type DevServer struct {
	cmd              *exec.Cmd
	client           client.Client
	frontendHostPort string
}

// StartDevServer starts a Temporal CLI dev server process. This may download the server if not already downloaded.
func StartDevServer(ctx context.Context, options DevServerOptions) (*DevServer, error) {
	clientOptions := options.clientOptionsOrDefault()

	exePath, err := downloadIfNeeded(ctx, &options, clientOptions.Logger)
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

	args := prepareCommand(&options, host, port, clientOptions.Namespace)

	cmd := newCmd(exePath, args...)
	if options.Stdout != nil {
		cmd.Stdout = options.Stdout
	}
	if options.Stderr != nil {
		cmd.Stderr = options.Stderr
	}

	clientOptions.Logger.Info("Starting DevServer", "ExePath", exePath, "Args", args)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed starting: %w", err)
	}

	returnedClient, err := waitServerReady(ctx, clientOptions)
	if err != nil {
		return nil, err
	}
	clientOptions.Logger.Info("DevServer ready")
	return &DevServer{
		client:           returnedClient,
		cmd:              cmd,
		frontendHostPort: clientOptions.HostPort,
	}, nil
}

func prepareCommand(options *DevServerOptions, host, port, namespace string) []string {
	args := []string{
		"server",
		"start-dev",
		"--ip", host, "--port", port,
		"--namespace", namespace,
		"--dynamic-config-value", "frontend.enableServerVersionCheck=false",
	}
	if options.LogLevel != "" {
		args = append(args, "--log-level", options.LogLevel)
	}
	if options.LogFormat != "" {
		args = append(args, "--log-format", options.LogFormat)
	}
	if !options.EnableUI {
		args = append(args, "--headless")
	}
	if options.DBFilename != "" {
		args = append(args, "--db-filename", options.DBFilename)
	}
	if options.UIPort != "" {
		args = append(args, "--ui-port", options.UIPort)
	}
	for searchAttribute := range options.SearchAttributes.GetUntypedValues() {
		args = append(args, "--search-attribute", searchAttribute.GetName()+"="+searchAttribute.GetValueType().String())
	}
	return append(args, options.ExtraArgs...)
}

func downloadIfNeeded(ctx context.Context, options *DevServerOptions, logger log.Logger) (string, error) {
	if options.ExistingPath != "" {
		return options.ExistingPath, nil
	}
	version := options.CachedDownload.Version
	if version == "" {
		version = "default"
	}
	destDir := options.CachedDownload.DestDir
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

	httpClient := &http.Client{Timeout: cliRequestTimeout}

	// Build info URL
	platform := runtime.GOOS
	if platform != "windows" && platform != "darwin" && platform != "linux" {
		return "", fmt.Errorf("unsupported platform %v", platform)
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported architecture %v", arch)
	}
	infoURL := fmt.Sprintf("https://temporal.download/cli/%v?platform=%v&arch=%v&sdk-name=sdk-go&sdk-version=%v", url.QueryEscape(version), platform, arch, internal.SDKVersion)

	// Get info
	info := struct {
		ArchiveURL    string `json:"archiveUrl"`
		FileToExtract string `json:"fileToExtract"`
	}{}
	var metadata []byte
	if err := requestCLI(ctx, httpClient, infoURL, logger, func(body io.Reader) error {
		var err error
		metadata, err = io.ReadAll(body)
		return err
	}); err != nil {
		return "", fmt.Errorf("failed fetching CLI archive info: %w", err)
	}
	if err := json.Unmarshal(metadata, &info); err != nil {
		return "", fmt.Errorf("failed unmarshaling info: %w", err)
	}

	// Download and extract
	logger.Info("Downloading temporal CLI", "Url", info.ArchiveURL, "ExePath", exePath)
	if err := requestCLI(ctx, httpClient, info.ArchiveURL, logger, func(body io.Reader) error {
		return installDownloadedCLI(body, info.ArchiveURL, info.FileToExtract, exePath, logger)
	}); err != nil {
		return "", fmt.Errorf("failed downloading Temporal CLI: %w", err)
	}
	return exePath, nil
}

func requestCLI(ctx context.Context, client *http.Client, address string, logger log.Logger, consume func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}

	for attempt := 1; ; attempt++ {
		resp, err := client.Do(req)
		retryable := err != nil
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				err = consume(cliResponseReader{resp.Body})
				var readErr cliNetworkReadError
				retryable = errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &readErr)
			} else {
				err = fmt.Errorf("HTTP status %d from %s", resp.StatusCode, req.URL)
				retryable = resp.StatusCode == http.StatusRequestTimeout ||
					resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				logger.Warn("Failed to close CLI response body", "Error", closeErr)
			}
		}

		if err == nil || !retryable || attempt == cliDownloadAttempts {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Debug("Retrying Temporal CLI request", "Attempt", attempt, "Error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
		}
	}
}

type cliResponseReader struct{ io.Reader }

func (r cliResponseReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == nil {
		return n, nil
	}
	// Filesystem errors can also implement net.Error. Classify it at the HTTP body boundary.
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return n, cliNetworkReadError{err}
	}
	return n, err
}

type cliNetworkReadError struct{ error }

func (e cliNetworkReadError) Unwrap() error { return e.error }

func installDownloadedCLI(body io.Reader, archiveURL, fileToExtract, exePath string, logger log.Logger) error {
	// Concurrent downloads use separate files beside the cached executable.
	// A same-filesystem rename puts only a complete download at the cache path.
	f, err := os.CreateTemp(filepath.Dir(exePath), "temporal-cli-downloading-")
	if err != nil {
		return fmt.Errorf("failed creating temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(f.Name()); err != nil && !os.IsNotExist(err) {
			logger.Warn("Failed to remove temporary CLI download", "Error", err)
		}
	}()
	if strings.HasSuffix(archiveURL, ".tar.gz") {
		err = extractTarball(body, fileToExtract, f)
	} else if strings.HasSuffix(archiveURL, ".zip") {
		err = extractZip(body, fileToExtract, f)
	} else {
		err = fmt.Errorf("unrecognized file extension on %v", archiveURL)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	} else if closeErr != nil {
		return fmt.Errorf("failed to close temp file: %w", closeErr)
	}
	// Chmod it if not Windows
	if runtime.GOOS != "windows" {
		if err := os.Chmod(f.Name(), 0755); err != nil {
			return fmt.Errorf("failed chmod'ing file: %w", err)
		}
	}
	return moveDownloadedCLI(f.Name(), exePath)
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

func (opts *DevServerOptions) clientOptionsOrDefault() client.Options {
	var out client.Options
	if opts.ClientOptions != nil {
		// Shallow copy the client options since we intend to overwrite some fields.
		out = *opts.ClientOptions
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
	lastErr := retryFor(ctx, 600, 100*time.Millisecond, func() error {
		var err error
		returnedClient, err = client.DialContext(ctx, options)
		return err
	})
	if lastErr != nil {
		return nil, fmt.Errorf("failed connecting after timeout, last error: %w", lastErr)
	}
	return returnedClient, lastErr
}

// retryFor retries some function until it returns nil or runs out of attempts. Wait interval between attempts.
func retryFor(ctx context.Context, maxAttempts int, interval time.Duration, cond func() error) error {
	if maxAttempts < 1 {
		// this is used internally, okay to panic
		panic("maxAttempts should be at least 1")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for range maxAttempts {
		if curE := cond(); curE == nil {
			return nil
		} else {
			lastErr = curE
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Try again after waiting up to interval.
		}
	}
	return lastErr
}

// Stop the running server and wait for shutdown to complete. Error is propagated from server shutdown.
func (s *DevServer) Stop() error {
	if err := sendInterrupt(s.cmd.Process); err != nil {
		return err
	}
	return s.cmd.Wait()
}

// Get a connected client, configured to work with the dev server.
func (s *DevServer) Client() client.Client {
	return s.client
}

// FrontendHostPort returns the host:port for this server.
func (s *DevServer) FrontendHostPort() string {
	return s.frontendHostPort
}
