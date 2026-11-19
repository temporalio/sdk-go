package awssdkv2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/contrib/aws/s3driver"
	"go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2"
	"go.temporal.io/sdk/converter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeS3 starts an in-process fake S3 server and returns an
// s3driver.Client backed by a real AWS SDK v2 client pointing
// at it. The returned cleanup function stops the server.
func newFakeS3(t *testing.T, buckets ...string) s3driver.Client {
	t.Helper()
	backend := s3mem.New()
	faker := gofakes3.New(backend)
	ts := httptest.NewServer(faker.Server())

	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(ts.URL),
		Region:       "ap-southeast-2",
		UsePathStyle: true,
		Credentials: aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     "test",
				SecretAccessKey: "test",
			}, nil
		}),
	})

	ctx := context.Background()
	for _, b := range buckets {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b)})
		require.NoError(t, err)
	}

	t.Cleanup(ts.Close)
	return awssdkv2.NewClient(client)
}

func TestAwsSdkClient_PutGetRoundTrip(t *testing.T) {
	client := newFakeS3(t, "test-bucket")
	ctx := context.Background()

	data := []byte("hello s3")
	err := client.PutObject(ctx, "test-bucket", "my/key", data)
	require.NoError(t, err)

	got, err := client.GetObject(ctx, "test-bucket", "my/key")
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestAwsSdkClient_ObjectExists(t *testing.T) {
	client := newFakeS3(t, "test-bucket")
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

func TestAwsSdkClient_GetObject_NotFound(t *testing.T) {
	client := newFakeS3(t, "test-bucket")
	ctx := context.Background()

	_, err := client.GetObject(ctx, "test-bucket", "no-such-key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NoSuchKey")
}

func TestAwsSdkClient_PutObject_BucketNotFound(t *testing.T) {
	client := newFakeS3(t)
	ctx := context.Background()

	err := client.PutObject(ctx, "no-such-bucket", "my/key", []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NoSuchBucket")
}

func TestAwsSdkClient_ObjectExists_BucketNotFound(t *testing.T) {
	client := newFakeS3(t)
	ctx := context.Background()

	// gofakes3 returns a generic NotFound for HeadObject on a missing bucket,
	// which ObjectExists maps to (false, nil) — the same as a missing key.
	// Real S3 would return NoSuchBucket here instead.
	exists, err := client.ObjectExists(ctx, "no-such-bucket", "my/key")
	assert.NoError(t, err)
	assert.False(t, exists)
}

func TestAwsSdkClient_GetObject_BucketNotFound(t *testing.T) {
	client := newFakeS3(t)
	ctx := context.Background()

	_, err := client.GetObject(ctx, "no-such-bucket", "my/key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NoSuchBucket")
}

func TestAwsSdkClient_LargeObject(t *testing.T) {
	client := newFakeS3(t, "test-bucket")
	ctx := context.Background()

	// 1 MiB of data.
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	err := client.PutObject(ctx, "test-bucket", "large-obj", data)
	require.NoError(t, err)

	got, err := client.GetObject(ctx, "test-bucket", "large-obj")
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestAwsSdkClient_Describe_ReturnsClientRegion(t *testing.T) {
	client := newFakeS3(t)
	assert.Equal(t, map[string]string{"client_region": "ap-southeast-2"}, client.Describe())
}

func TestAwsSdkClient_Describe_EmptyRegion(t *testing.T) {
	s3c := s3.New(s3.Options{
		BaseEndpoint: aws.String("http://localhost"),
		UsePathStyle: true,
		Credentials: aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
		}),
	})
	client := awssdkv2.NewClient(s3c)
	assert.Nil(t, client.Describe())
}

// TestAwsSdkClient_FullDriverRoundTrip exercises the S3StorageDriver end-to-end
// through the fake S3 backend.
func TestAwsSdkClient_FullDriverRoundTrip(t *testing.T) {
	client := newFakeS3(t, "driver-bucket")

	d, err := s3driver.NewDriver(s3driver.Options{
		Client: client,
		Bucket: s3driver.StaticBucket("driver-bucket"),
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
