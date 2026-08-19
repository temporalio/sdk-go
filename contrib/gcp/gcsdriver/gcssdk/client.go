package gcssdk

import (
	"context"
	"errors"
	"io"

	"cloud.google.com/go/storage"
	"go.temporal.io/sdk/contrib/gcp/gcsdriver"
)

type gcsClient struct {
	client *storage.Client
}

// NewClient creates a gcsdriver.Client backed by a Google Cloud Storage client.
//
// NOTE: Experimental
func NewClient(client *storage.Client) gcsdriver.Client {
	return &gcsClient{client: client}
}

func (c *gcsClient) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	w := c.client.Bucket(bucket).Object(key).NewWriter(ctx)
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (c *gcsClient) ObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := c.client.Bucket(bucket).Object(key).Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			// The GCS Go client returns ErrObjectNotExist for both a
			// missing object in a valid bucket and for any object in a
			// missing bucket. We return (false, nil) in both cases to
			// align with the S3 driver's behavior, where HeadObject on
			// a missing bucket also maps to a generic NotFound.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *gcsClient) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	r, err := c.client.Bucket(bucket).Object(key).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

func (c *gcsClient) Describe() map[string]string {
	// GCS client doesn't expose metadata synchronously via client options.
	return nil
}
