// Package cloudrun provides helpers for identifying a Temporal worker that runs on
// Google Cloud Run, covering both Cloud Run worker pools and Cloud Run services.
//
// Unlike AWS Lambda, Cloud Run runs a long-lived container: there is no per-invocation
// handler to wrap, so this package is a small metadata helper rather than a worker wrapper.
// Read the instance metadata once at startup with [FetchMetadata], then apply it to your own,
// normal long-lived worker:
//
//   - [Metadata.ApplyToClientOptions] sets the derived client identity on your
//     [go.temporal.io/sdk/client.Options] (a user-set identity always wins).
//   - [Metadata.ApplyToWorkerOptions] enables Worker Deployment Versioning on your
//     [go.temporal.io/sdk/worker.Options] using the Cloud Run deployment version.
//
// The lower-level [Metadata.WorkerIdentity] and [Metadata.DeploymentVersion] accessors are also
// available if you prefer to wire the values in yourself.
//
// # Experimental
//
// Google Cloud Run support is experimental and its API may change in a future release.
//
// # Usage
//
//	func main() {
//	    md, err := cloudrun.FetchMetadata(context.Background())
//	    if err != nil {
//	        log.Fatalf("fetching Cloud Run metadata: %v", err)
//	    }
//
//	    clientOptions := client.Options{}
//	    md.ApplyToClientOptions(&clientOptions)
//	    c, err := client.Dial(clientOptions)
//	    if err != nil {
//	        log.Fatalf("dialing Temporal server: %v", err)
//	    }
//	    defer c.Close()
//
//	    workerOptions := worker.Options{}
//	    if err := md.ApplyToWorkerOptions(&workerOptions); err != nil {
//	        log.Fatalf("configuring worker versioning: %v", err)
//	    }
//	    w := worker.New(c, "my-task-queue", workerOptions)
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
	"context"
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// Example shows how to use the Cloud Run metadata helper to configure a normal, long-lived
// Temporal worker: it applies the derived client identity and Worker Deployment Version, read from
// the Cloud Run instance metadata at startup, to the client and worker options.
func Example() {
	ctx := context.Background()

	// Read Cloud Run instance metadata once, at worker startup. Do not call FetchMetadata from
	// workflow code: it performs a network request, which the SDK's workflowcheck analyzer flags
	// inside workflows.
	md, err := FetchMetadata(ctx)
	if err != nil {
		log.Fatalf("fetching Cloud Run metadata: %v", err)
	}

	// Apply the derived worker identity to the client options. A user-set identity always wins.
	clientOptions := client.Options{}
	md.ApplyToClientOptions(&clientOptions)

	c, err := client.Dial(clientOptions)
	if err != nil {
		log.Fatalf("dialing Temporal server: %v", err)
	}
	defer c.Close()

	// Apply Worker Deployment Versioning (deployment name + build ID) to the worker options.
	workerOptions := worker.Options{}
	if err := md.ApplyToWorkerOptions(&workerOptions); err != nil {
		log.Fatalf("configuring worker versioning: %v", err)
	}

	w := worker.New(c, "my-task-queue", workerOptions)

	// Register your workflows and activities on w here, then run the long-lived worker.
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("running worker: %v", err)
	}
}
