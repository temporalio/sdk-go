package gcssdk_test

import (
	"context"
	"io"
	"testing"

	"github.com/fsouza/fake-gcs-server/fakestorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/contrib/gcp/gcsdriver"
	"go.temporal.io/sdk/contrib/gcp/gcsdriver/gcssdk"
	"go.temporal.io/sdk/converter"
)

// newFakeGCS starts an in-process fake GCS server and returns a
// gcsdriver.Client backed by a real Google Cloud Storage client pointing
// at it. The returned client is configured to use the fake server.
func newFakeGCS(t *testing.T, buckets ...string) gcsdriver.Client {
	t.Helper()
	server := fakestorage.NewServer(nil)
	t.Cleanup(server.Stop)

	for _, b := range buckets {
		server.CreateBucketWithOpts(fakestorage.CreateBucketOpts{Name: b})
	}

	return gcssdk.NewClient(server.Client())
}

func TestGcsSdkClient_PutGetRoundTrip(t *testing.T) {
	client := newFakeGCS(t, "test-bucket")
	ctx := context.Background()

	data := []byte("hello gcs")
	err := client.PutObject(ctx, "test-bucket", "my/key", data)
	require.NoError(t, err)

	gotReader, err := client.GetObject(ctx, "test-bucket", "my/key")
	require.NoError(t, err)
	defer gotReader.Close()
	got, err := io.ReadAll(gotReader)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestGcsSdkClient_ObjectExists(t *testing.T) {
	client := newFakeGCS(t, "test-bucket")
	ctx := context.Background()

	exists, err := client.ObjectExists(ctx, "test-bucket", "missing-key")
	require.NoError(t, err)
	assert.False(t, exists)

	err = client.PutObject(ctx, "test-bucket", "present-key", []byte("data"))
	require.NoError(t, err)

	exists, err = client.ObjectExists(ctx, "test-bucket", "present-key")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestGcsSdkClient_GetObject_NotFound(t *testing.T) {
	client := newFakeGCS(t, "test-bucket")
	ctx := context.Background()

	_, err := client.GetObject(ctx, "test-bucket", "no-such-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "object doesn't exist")
}

func TestGcsSdkClient_PutObject_BucketNotFound(t *testing.T) {
	client := newFakeGCS(t)
	ctx := context.Background()

	err := client.PutObject(ctx, "no-such-bucket", "my/key", []byte("data"))
	require.Error(t, err)
}

func TestGcsSdkClient_ObjectExists_BucketNotFound(t *testing.T) {
	client := newFakeGCS(t)
	ctx := context.Background()

	// When the bucket does not exist, ObjectExists returns (false, nil)
	// to align with the S3 driver's behavior. The GCS Go client returns
	// ErrObjectNotExist for any object in a missing bucket, and we treat
	// that the same as a genuinely missing object.
	exists, err := client.ObjectExists(ctx, "no-such-bucket", "my/key")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestGcsSdkClient_GetObject_BucketNotFound(t *testing.T) {
	client := newFakeGCS(t)
	ctx := context.Background()

	_, err := client.GetObject(ctx, "no-such-bucket", "my/key")
	require.Error(t, err)
}

func TestGcsSdkClient_LargeObject(t *testing.T) {
	client := newFakeGCS(t, "test-bucket")
	ctx := context.Background()

	// 1 MiB of data.
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	err := client.PutObject(ctx, "test-bucket", "large-obj", data)
	require.NoError(t, err)

	gotReader, err := client.GetObject(ctx, "test-bucket", "large-obj")
	require.NoError(t, err)
	defer gotReader.Close()
	got, err := io.ReadAll(gotReader)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestGcsSdkClient_Describe(t *testing.T) {
	client := newFakeGCS(t)
	assert.Nil(t, client.Describe())
}

// TestGcsSdkClient_FullDriverRoundTrip exercises the gcsStorageDriver end-to-end
// through the fake GCS backend.
func TestGcsSdkClient_FullDriverRoundTrip(t *testing.T) {
	client := newFakeGCS(t, "driver-bucket")

	d, err := gcsdriver.NewDriver(gcsdriver.Options{
		Client: client,
		Bucket: gcsdriver.StaticBucket("driver-bucket"),
	})
	require.NoError(t, err)

	payloads := []*commonpb.Payload{
		{Metadata: map[string][]byte{"encoding": []byte("binary/plain")}, Data: []byte("integration-test-1")},
		{Metadata: map[string][]byte{"encoding": []byte("binary/plain")}, Data: []byte("integration-test-2")},
	}

	claims, err := d.Store(
		converter.StorageDriverStoreContext{Context: context.Background()},
		payloads,
	)
	require.NoError(t, err)
	require.Len(t, claims, 2)

	restored, err := d.Retrieve(
		converter.StorageDriverRetrieveContext{Context: context.Background()},
		claims,
	)
	require.NoError(t, err)
	require.Len(t, restored, 2)

	for i := range payloads {
		assert.Equal(t, payloads[i].Data, restored[i].Data)
	}
}
