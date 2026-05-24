package gcssdk

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"cloud.google.com/go/storage"
	"go.temporal.io/sdk/contrib/google/gcsdriver"
)

type gcsClient struct {
	client        *storage.Client
	bucketChecked sync.Map // bucket name → struct{}; caches successful bucket-existence checks
}

// NewClient creates a gcsdriver.Client backed by a Google Cloud Storage client.
//
// NOTE: Experimental
func NewClient(client *storage.Client) gcsdriver.Client {
	return &gcsClient{client: client}
}

func (c *gcsClient) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	w := c.client.Bucket(bucket).Object(key).NewWriter(ctx)
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
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
			// missing bucket. If we haven't already confirmed this
			// bucket exists, perform an explicit Bucket.Attrs() call
			// to distinguish the two cases. The result is cached so
			// subsequent misses in the same bucket avoid the extra RPC.
			if _, ok := c.bucketChecked.Load(bucket); !ok {
				if _, bErr := c.client.Bucket(bucket).Attrs(ctx); bErr != nil {
					return false, bErr
				}
				c.bucketChecked.Store(bucket, struct{}{})
			}
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
	defer r.Close()
	return io.ReadAll(r)
}

func (c *gcsClient) Describe() map[string]string {
	// GCS client doesn't expose metadata synchronously via client options.
	return nil
}
