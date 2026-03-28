package s3driver

import "context"

// Client is the interface that the driver uses to interact with S3. It covers
// the three operations the driver needs: put, existence check, and get.
// Use [go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2.NewClient] to obtain
// an implementation backed by the AWS SDK v2, or supply a custom
// implementation for testing or alternative S3-compatible storage.
//
// NOTE: Experimental
type Client interface {
	// PutObject uploads data to the given bucket and key.
	PutObject(ctx context.Context, bucket, key string, data []byte) error

	// ObjectExists returns true if an object exists at the given bucket and key.
	ObjectExists(ctx context.Context, bucket, key string) (bool, error)

	// GetObject downloads and returns the data stored at the given bucket and key.
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
}
