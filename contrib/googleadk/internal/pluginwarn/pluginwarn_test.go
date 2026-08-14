// Package pluginwarn_test proves NewPlugin wires the raw-global-provider
// warning into both of its hooks: worker start and workflow replayer creation.
//
// It is deliberately a separate (test-only) package: `go test` runs each
// package's tests in its own process, and these tests must install a raw OTel
// SDK provider as a process global. The OTel global proxy binds its delegate
// on the first Set*Provider call permanently, so doing that inside the main
// googleadk test binary would poison the switchable globals that package's
// replay-telemetry tests install. A fresh test process is the only clean way
// to exercise the real global-install path end to end.
package pluginwarn_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"go.temporal.io/sdk/contrib/googleadk"
)

// noopRegistry satisfies the worker registry interface StartWorker requires.
type noopRegistry struct{}

func (noopRegistry) RegisterWorkflowWithOptions(any, workflow.RegisterOptions)    {}
func (noopRegistry) RegisterDynamicWorkflow(any, workflow.DynamicRegisterOptions) {}
func (noopRegistry) RegisterActivityWithOptions(any, activity.RegisterOptions)    {}
func (noopRegistry) RegisterDynamicActivity(any, activity.DynamicRegisterOptions) {}
func (noopRegistry) RegisterNexusService(*nexus.Service)                          {}

func TestPluginWarnsOnRawGlobalProvider(t *testing.T) {
	// Raw and unwrapped — the misconfiguration the warning exists for. The
	// tracer and logger globals stay unbound proxies, which must not warn.
	otel.SetMeterProvider(sdkmetric.NewMeterProvider())

	// The plugin warns through the process-default slog.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	plugin, err := googleadk.NewPlugin(googleadk.Config{})
	require.NoError(t, err)

	t.Run("AtWorkerStart", func(t *testing.T) {
		buf.Reset()
		require.NoError(t, plugin.StartWorker(context.Background(),
			worker.PluginStartWorkerOptions{WorkerInstanceKey: "worker-1", WorkerRegistry: noopRegistry{}},
			func(context.Context, worker.PluginStartWorkerOptions) error { return nil },
		))
		assert.Equal(t, 1, strings.Count(buf.String(), "not replay-safe"),
			"exactly the raw meter provider must warn")
		assert.Contains(t, buf.String(), "NewReplaySafeMeterProvider")
	})

	t.Run("AtReplayerCreation", func(t *testing.T) {
		buf.Reset()
		_, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
			Plugins: []worker.Plugin{plugin},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(buf.String(), "not replay-safe"),
			"exactly the raw meter provider must warn")
		assert.Contains(t, buf.String(), "NewReplaySafeMeterProvider")
	})
}
