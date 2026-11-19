// Package s3driver provides an S3-backed [go.temporal.io/sdk/converter.StorageDriver]
// for the Temporal Go SDK's external payload storage system. Large payloads are
// offloaded to Amazon S3 using content-addressable keys derived from their
// SHA-256 hash.
//
// # Usage
//
// Construct a driver using [NewDriver] with an [Options] struct. The [Client]
// field accepts any implementation of the [Client] interface; use
// [go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2.NewClient] to wrap an
// AWS SDK v2 S3 client. The [Options.Bucket] field accepts a [BucketFunc] that
// resolves the target bucket per payload; use [StaticBucket] for a fixed name.
//
// NOTE: Experimental
package s3driver
