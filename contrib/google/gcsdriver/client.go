package gcsdriver

import "context"

// Client is the interface that the driver uses to interact with GCS. It covers
// the three operations the driver needs: put, existence check, and get.
// Use [go.temporal.io/sdk/contrib/google/gcsdriver/gcssdk.NewClient] to obtain
// an implementation backed by the Cloud Storage client, or supply a custom
// implementation for testing or alternative GCS-compatible storage.
//
// NOTE: Experimental
type Client interface {
	// PutObject uploads data to the given bucket and key. If an object already
	// exists at that key it should be overwritten. Implementations must be safe
	// to call concurrently for different keys.
	PutObject(ctx context.Context, bucket, key string, data []byte) error

	// ObjectExists reports whether an object exists at the given bucket and key.
	// It should return (false, nil) when the object is absent, and a non-nil
	// error only when the existence of the object cannot be determined (e.g. a
	// network or permission failure).
	ObjectExists(ctx context.Context, bucket, key string) (bool, error)

	// GetObject downloads and returns the data stored at the given bucket and
	// key. It must return a non-nil error if the object does not exist.
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)

	// Describe returns diagnostic metadata about the client configuration,
	// such as {"client_project_id": "my-project"}, that the driver appends to error
	// messages. Return nil or an empty map if no metadata is available.
	Describe() map[string]string
}
