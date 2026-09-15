// Package id configures a Temporal worker that runs on Google Cloud Run, covering both Cloud
// Run worker pools and Cloud Run services.
//
// The primary API is [CloudRunIDPlugin], a client plugin. Register it once on
// [go.temporal.io/sdk/client.Options.Plugins] and it sets the client
// [go.temporal.io/sdk/client.Options.Identity] to the Cloud Run-derived worker identity, unless a
// user-set identity is already present (a user-set identity always wins). Every worker created from
// the client inherits that identity.
//
// The plugin fetches the instance metadata once, when the client connects. If the fetch fails
// (usually because the process is not running on Cloud Run), client creation returns an error.
//
// The lower-level [FetchMetadata] reader and the [Metadata.Identity] accessor remain available
// if you prefer to wire the value in yourself.
//
// # Experimental
//
// Google Cloud Run support is experimental and its API may change in a future release.
//
// # Usage
//
//	func main() {
//	    // Register the Cloud Run plugin on the client. It fetches the instance metadata when the
//	    // client connects and sets the client identity.
//	    c, err := client.Dial(client.Options{
//	        Plugins: []client.Plugin{id.NewCloudRunIDPlugin()},
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
// The worker pool (or service) name and revision come from environment variables that Cloud Run
// injects into every container instance: CLOUD_RUN_WORKER_POOL and CLOUD_RUN_REVISION on worker
// pools, or K_SERVICE and K_REVISION on services. The unique instance ID is only available from the GCP
// metadata server, which [FetchMetadata] queries over HTTP; the metadata server is available on
// both worker pools and services.
package id

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// Example shows how to configure Temporal worker on Cloud Run with the plugin.
// Registering [CloudRunIDPlugin] on the client sets the derived client identity, read from the instance
// metadata when the client connects.
func Example() {
	// Register the Cloud Run plugin on the client. It fetches the instance metadata when the client
	// connects (never from workflow code) and sets the derived client identity unless one is already
	// set.
	plugin := NewCloudRunIDPlugin()

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
