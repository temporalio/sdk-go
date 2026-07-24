package opentelemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	opentelemetry "go.temporal.io/sdk/contrib/opentelemetry-v2"
)

func TestNewPlugin(t *testing.T) {
	plugin, shutdown, err := opentelemetry.NewPlugin(opentelemetry.PluginOptions{})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	_ = client.Options{Plugins: []client.Plugin{plugin}}
	_ = worker.WorkflowReplayerOptions{Plugins: []worker.Plugin{plugin}}

	require.NoError(t, shutdown(context.Background()))
}
