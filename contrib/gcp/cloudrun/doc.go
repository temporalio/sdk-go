// Package cloudrun configures a Temporal worker that runs on Google Cloud Run, covering both Cloud
// Run worker pools and Cloud Run services.
//
// Unlike AWS Lambda, Cloud Run runs a long-lived container: there is no per-invocation handler to
// wrap. The primary API is [Plugin], a client-and-worker plugin. Register it once on
// [go.temporal.io/sdk/client.Options.Plugins] and it automatically propagates to every worker
// created from the client, where it:
//
//   - sets the client [go.temporal.io/sdk/client.Options.Identity] to the Cloud Run-derived worker
//     identity, unless a user-set identity is already present (a user-set identity always wins), and
//   - opts each worker into Worker Deployment Versioning via
//     [go.temporal.io/sdk/worker.Options.DeploymentOptions], using the Cloud Run deployment version
//     and pinning workflows to it by default.
//
// The plugin fetches the instance metadata once, when the client connects. If the fetch fails —
// typically because the process is not running on a Cloud Run worker pool or service — client
// creation fails with a clear error.
//
// The lower-level [FetchMetadata] reader and the [Metadata.WorkerIdentity] and
// [Metadata.DeploymentVersion] accessors remain available if you prefer to wire the values in
// yourself (or to inject metadata into the plugin via [PluginOptions.Metadata]).
//
// # Experimental
//
// Google Cloud Run support is experimental and its API may change in a future release.
//
// # Usage
//
//	func main() {
//	    // Register the Cloud Run plugin on the client. It fetches the instance metadata when the
//	    // client connects, sets the worker identity, and pins each worker's deployment version.
//	    c, err := client.Dial(client.Options{
//	        Plugins: []client.Plugin{cloudrun.NewPlugin(cloudrun.PluginOptions{})},
//	    })
//	    if err != nil {
//	        log.Fatalf("dialing Temporal server: %v", err)
//	    }
//	    defer c.Close()
//
//	    w := worker.New(c, "my-task-queue", worker.Options{})
//	    // Register your workflows and activities on w here.
//	    if err := w.Run(worker.InterruptCh()); err != nil {
//	        log.Fatalf("running worker: %v", err)
//	    }
//	}
//
// # Metadata source
//
// The deployment name and revision come from environment variables that Cloud Run injects into
// every container instance: CLOUD_RUN_WORKER_POOL and CLOUD_RUN_REVISION on worker pools, or
// K_SERVICE and K_REVISION on services. The unique instance ID is only available from the GCP
// metadata server, which [FetchMetadata] queries over HTTP; the metadata server is available on
// both worker pools and services.
//
// Because [FetchMetadata] performs a network request, call it at worker startup and never from
// workflow code: the SDK's workflowcheck analyzer flags net/http usage inside workflows because it
// is non-deterministic.
package cloudrun

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// Example shows how to configure a normal, long-lived Temporal worker on Cloud Run with the plugin.
// Registering [Plugin] on the client sets the derived worker identity and pins each worker to the
// Cloud Run deployment version, all read from the instance metadata when the client connects.
func Example() {
	// Register the Cloud Run plugin on the client. It fetches the instance metadata when the client
	// connects (never from workflow code), sets the derived worker identity unless one is already
	// set, and opts each worker created from the client into PINNED Worker Deployment Versioning.
	plugin := NewPlugin(PluginOptions{})

	c, err := client.Dial(client.Options{
		Plugins: []client.Plugin{plugin},
	})
	if err != nil {
		log.Fatalf("dialing Temporal server: %v", err)
	}
	defer c.Close()

	w := worker.New(c, "my-task-queue", worker.Options{})

	// Register your workflows and activities on w here, then run the long-lived worker.
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("running worker: %v", err)
	}
}
