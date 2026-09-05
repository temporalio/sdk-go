package workerid_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/gcp/cloudrun/workerid"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// completeMetadata is a fully populated Cloud Run metadata value, as the plugin sees it after a
// successful fetch on a Cloud Run worker pool.
var completeMetadata = &workerid.Metadata{InstanceID: "i-1", Name: "my-pool", Revision: "rev-1"}

// TestPlugin_ImplementsInterfaces confirms the plugin satisfies both the client and worker plugin
// interfaces, so it can be registered once on client.Options.Plugins and propagate to workers.
func TestPlugin_ImplementsInterfaces(t *testing.T) {
	plugin := workerid.NewPlugin(workerid.PluginOptions{Metadata: completeMetadata})
	var (
		_ client.Plugin = plugin
		_ worker.Plugin = plugin
	)
	// It is usable in the Plugins slice a caller would build.
	_ = client.Options{Plugins: []client.Plugin{plugin}}
	assert.Equal(t, "temporal-cloudrun-worker-id", plugin.Name())
}

// TestPlugin_ConfigureClient_SetsIdentityWhenUnset covers the client hook: it sets the derived
// worker identity only when the caller has not set one, so a user-provided identity always wins.
func TestPlugin_ConfigureClient_SetsIdentityWhenUnset(t *testing.T) {
	plugin := workerid.NewPlugin(workerid.PluginOptions{Metadata: completeMetadata})

	t.Run("sets identity when unset", func(t *testing.T) {
		o := client.Options{}
		require.NoError(t, plugin.ConfigureClient(context.Background(),
			client.PluginConfigureClientOptions{ClientOptions: &o}))
		assert.Equal(t, "i-1@rev-1", o.Identity)
	})

	t.Run("preserves a user-provided identity", func(t *testing.T) {
		o := client.Options{Identity: "user-identity"}
		require.NoError(t, plugin.ConfigureClient(context.Background(),
			client.PluginConfigureClientOptions{ClientOptions: &o}))
		assert.Equal(t, "user-identity", o.Identity)
	})
}

// TestPlugin_ConfigureClient_NilOptions covers the guard against missing client options.
func TestPlugin_ConfigureClient_NilOptions(t *testing.T) {
	plugin := workerid.NewPlugin(workerid.PluginOptions{Metadata: completeMetadata})
	err := plugin.ConfigureClient(context.Background(), client.PluginConfigureClientOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloudrun:")
}

// TestPlugin_ConfigureWorker_SetsPinnedDeploymentOptions covers the worker hook: it enables Worker
// Deployment Versioning with the Cloud Run deployment version and a PINNED default behavior.
func TestPlugin_ConfigureWorker_SetsPinnedDeploymentOptions(t *testing.T) {
	plugin := workerid.NewPlugin(workerid.PluginOptions{Metadata: completeMetadata})

	o := worker.Options{}
	require.NoError(t, plugin.ConfigureWorker(context.Background(),
		worker.PluginConfigureWorkerOptions{WorkerOptions: &o}))

	assert.True(t, o.DeploymentOptions.UseVersioning)
	assert.Equal(t,
		worker.WorkerDeploymentVersion{DeploymentName: "my-pool", BuildID: "rev-1"},
		o.DeploymentOptions.Version)
	assert.Equal(t, workflow.VersioningBehaviorPinned, o.DeploymentOptions.DefaultVersioningBehavior)
}

// TestPlugin_ConfigureWorker_LeavesOptionsUnchangedWithoutMetadata covers the worker hook's
// no-panic contract: worker.New turns a ConfigureWorker error into a panic, so when metadata is
// missing or incomplete the hook leaves the options unchanged and returns nil rather than erroring.
func TestPlugin_ConfigureWorker_LeavesOptionsUnchangedWithoutMetadata(t *testing.T) {
	t.Run("no metadata fetched yet", func(t *testing.T) {
		plugin := workerid.NewPlugin(workerid.PluginOptions{}) // ConfigureClient never called
		o := worker.Options{}
		require.NoError(t, plugin.ConfigureWorker(context.Background(),
			worker.PluginConfigureWorkerOptions{WorkerOptions: &o}))
		assert.Equal(t, worker.DeploymentOptions{}, o.DeploymentOptions)
	})

	t.Run("incomplete metadata (missing revision)", func(t *testing.T) {
		plugin := workerid.NewPlugin(workerid.PluginOptions{
			Metadata: &workerid.Metadata{InstanceID: "i-1", Name: "my-pool"},
		})
		o := worker.Options{}
		require.NoError(t, plugin.ConfigureWorker(context.Background(),
			worker.PluginConfigureWorkerOptions{WorkerOptions: &o}))
		assert.Equal(t, worker.DeploymentOptions{}, o.DeploymentOptions)
	})

	t.Run("nil worker options", func(t *testing.T) {
		plugin := workerid.NewPlugin(workerid.PluginOptions{Metadata: completeMetadata})
		require.NoError(t, plugin.ConfigureWorker(context.Background(),
			worker.PluginConfigureWorkerOptions{}))
	})
}

// TestPlugin_FetchesAndCachesFromMetadataServer covers the real fetch path: with the Cloud Run
// environment variables set and a stub metadata server, ConfigureClient reads the instance ID over
// HTTP, sets the identity, and caches the metadata for the worker hook (and Metadata accessor).
func TestPlugin_FetchesAndCachesFromMetadataServer(t *testing.T) {
	t.Setenv("CLOUD_RUN_WORKER_POOL", "my-pool")
	t.Setenv("CLOUD_RUN_REVISION", "my-pool-00007-abc")
	t.Setenv("K_SERVICE", "")
	t.Setenv("K_REVISION", "")

	srv := newStubMetadataServer(http.StatusOK, testInstanceID)
	defer srv.Close()

	plugin := workerid.NewPlugin(workerid.PluginOptions{MetadataURL: srv.URL})

	// Before connect, nothing is fetched.
	assert.Nil(t, plugin.Metadata())

	co := client.Options{}
	require.NoError(t, plugin.ConfigureClient(context.Background(),
		client.PluginConfigureClientOptions{ClientOptions: &co}))
	assert.Equal(t, testInstanceID+"@my-pool-00007-abc", co.Identity)

	// The metadata is cached and drives the worker hook without a second fetch.
	md := plugin.Metadata()
	require.NotNil(t, md)
	assert.Equal(t, testInstanceID, md.InstanceID)
	assert.Equal(t, "my-pool", md.Name)

	wo := worker.Options{}
	require.NoError(t, plugin.ConfigureWorker(context.Background(),
		worker.PluginConfigureWorkerOptions{WorkerOptions: &wo}))
	assert.Equal(t,
		worker.WorkerDeploymentVersion{DeploymentName: "my-pool", BuildID: "my-pool-00007-abc"},
		wo.DeploymentOptions.Version)
}

// TestPlugin_ConfigureClient_FailsFastOffPlatform covers the off-platform behavior: when the
// metadata server is unreachable (the process is not on a Cloud Run worker pool or service),
// ConfigureClient fails fast with a clear error and leaves the identity unset.
func TestPlugin_ConfigureClient_FailsFastOffPlatform(t *testing.T) {
	// Start a stub server and immediately close it so its URL is unreachable.
	srv := newStubMetadataServer(http.StatusOK, testInstanceID)
	url := srv.URL
	srv.Close()

	plugin := workerid.NewPlugin(workerid.PluginOptions{
		MetadataURL: url,
		HTTPClient:  &http.Client{Timeout: time.Second},
	})

	o := client.Options{}
	err := plugin.ConfigureClient(context.Background(),
		client.PluginConfigureClientOptions{ClientOptions: &o})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloudrun:")
	assert.Empty(t, o.Identity)
}
