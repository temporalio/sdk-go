// Package gcsdriver provides a GCS-backed [go.temporal.io/sdk/converter.StorageDriver]
// for the Temporal Go SDK's external payload storage system. Large payloads are
// offloaded to Google Cloud Storage using content-addressable keys derived from their
// SHA-256 hash.
//
// # Usage
//
// Construct a driver using [NewDriver] with an [Options] struct. The [Client]
// field accepts any implementation of the [Client] interface; use
// [go.temporal.io/sdk/contrib/google/gcsdriver/gcssdk.NewClient] to wrap a
// Cloud Storage client. The [Options.Bucket] field accepts a [BucketFunc] that
// resolves the target bucket per payload; use [StaticBucket] for a fixed name.
//
// NOTE: Experimental
package gcsdriver
