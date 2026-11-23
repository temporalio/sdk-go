package testsuite

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"time"

	"go.temporal.io/sdk/client"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/internal/temporalcli"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
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
	return temporalcli.Download(ctx, options.CachedDownload.Version, options.CachedDownload.DestDir, logger)
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
