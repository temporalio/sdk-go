package google_adk_agents_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/testsuite"
)

// devServerBinaryFallback is the common Homebrew install location for the
// Temporal CLI dev server, consulted when "temporal" is not on PATH.
const devServerBinaryFallback = "/opt/homebrew/bin/temporal"

// devServerPath locates the Temporal dev server binary, skipping the test when
// it is absent. This is capability detection (the binary may not be installed
// in CI), NOT an operator-set env-var gate: where the binary exists the test
// always runs.
func devServerPath(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("temporal"); err == nil {
		return p
	}
	if _, err := os.Stat(devServerBinaryFallback); err == nil {
		return devServerBinaryFallback
	}
	t.Skip("temporal dev server binary not found on PATH or at " + devServerBinaryFallback + "; skipping integration test")
	return ""
}

// startDevServerOrSkip boots a local Temporal dev server wired with the
// plugin's data converter (so the genai/session types cross the real wire
// exactly as in production). It skips on a missing binary or a boot failure —
// both capability conditions, never an operator toggle.
func startDevServerOrSkip(t *testing.T) *testsuite.DevServer {
	t.Helper()
	path := devServerPath(t)
	srv, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{
		ExistingPath: path,
		LogLevel:     "error",
		ClientOptions: &client.Options{
			DataConverter: adk.NewDataConverter(),
		},
	})
	if err != nil {
		t.Skipf("could not start temporal dev server (%v); skipping integration test", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return srv
}

// testContext returns a context bounded so a hung server call fails the test
// rather than hanging the suite.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}
