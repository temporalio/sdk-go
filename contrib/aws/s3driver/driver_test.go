package s3driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// memClient is an in-memory S3StorageClient for unit testing.
type memClient struct {
	mu       sync.RWMutex
	data     map[string][]byte // key: "bucket/key"
	putCount atomic.Int64
}

func newMemClient() *memClient {
	return &memClient{data: make(map[string][]byte)}
}

func memKey(bucket, key string) string { return bucket + "/" + key }

func (m *memClient) PutObject(_ context.Context, bucket, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putCount.Add(1)
	cp := make([]byte, len(data))
	copy(cp, data)
	m.data[memKey(bucket, key)] = cp
	return nil
}

func (m *memClient) ObjectExists(_ context.Context, bucket, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[memKey(bucket, key)]
	return ok, nil
}

func (m *memClient) GetObject(_ context.Context, bucket, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.data[memKey(bucket, key)]
	if !ok {
		return nil, fmt.Errorf("not found: %s/%s", bucket, key)
	}
	cp := make([]byte, len(d))
	copy(cp, d)
	return cp, nil
}

func testPayload(data string) *commonpb.Payload {
	return &commonpb.Payload{
		Metadata: map[string][]byte{"encoding": []byte("binary/plain")},
		Data:     []byte(data),
	}
}

func newDriver(t *testing.T, client Client) converter.StorageDriver {
	t.Helper()
	d, err := NewDriver(Options{
		Client: client,
		Bucket: StaticBucket("test-bucket"),
	})
	require.NoError(t, err)
	return d
}

func storeCtx() converter.StorageDriverStoreContext {
	return converter.StorageDriverStoreContext{Context: context.Background()}
}

func retrieveCtx() converter.StorageDriverRetrieveContext {
	return converter.StorageDriverRetrieveContext{Context: context.Background()}
}

// --- Constructor tests ---

func TestNewS3StorageDriver_Defaults(t *testing.T) {
	mc := newMemClient()
	d, err := NewDriver(Options{
		Client: mc,
		Bucket: StaticBucket("b"),
	})
	require.NoError(t, err)
	typedDriver, ok := d.(*s3StorageDriver)
	require.True(t, ok, "expected *s3Driver, got %T", d)
	assert.Equal(t, "aws.s3driver", typedDriver.Name())
	assert.Equal(t, "aws.s3driver", typedDriver.Type())
	assert.Equal(t, 50*1024*1024, typedDriver.maxPayloadSize)
}

func TestNewS3StorageDriver_CustomName(t *testing.T) {
	mc := newMemClient()
	d, err := NewDriver(Options{
		Client:     mc,
		Bucket:     StaticBucket("b"),
		DriverName: "custom-name",
	})
	require.NoError(t, err)
	assert.Equal(t, "custom-name", d.Name())
}

func TestNewS3StorageDriver_NilClient(t *testing.T) {
	_, err := NewDriver(Options{
		Bucket: StaticBucket("b"),
	})
	assert.EqualError(t, err, "s3driver: Client is required")
}

func TestNewS3StorageDriver_NilBucketFunc(t *testing.T) {
	_, err := NewDriver(Options{
		Client: newMemClient(),
	})
	assert.EqualError(t, err, "s3driver: Bucket is required")
}

func TestNewS3StorageDriver_NegativeMaxPayloadSize(t *testing.T) {
	_, err := NewDriver(Options{
		Client:         newMemClient(),
		Bucket:         StaticBucket("b"),
		MaxPayloadSize: -1,
	})
	assert.EqualError(t, err, "s3driver: MaxPayloadSize must be positive, got -1")
}

// --- StaticBucket tests ---

func TestStaticBucket(t *testing.T) {
	fn := StaticBucket("my-bucket")
	assert.Equal(t, "my-bucket", fn(converter.StorageDriverStoreContext{Context: context.Background()}, nil))
	assert.Equal(t, "my-bucket", fn(converter.StorageDriverStoreContext{Context: context.Background()}, testPayload("x")))
}

// --- Store tests ---

func TestStore_SinglePayload(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)
	p := testPayload("hello")

	claims, err := d.Store(storeCtx(), []*commonpb.Payload{p})
	require.NoError(t, err)
	require.Len(t, claims, 1)

	assert.Equal(t, "test-bucket", claims[0].ClaimData["bucket"])
	assert.Equal(t, "sha256", claims[0].ClaimData["hash_algorithm"])
	assert.NotEmpty(t, claims[0].ClaimData["key"])
	assert.NotEmpty(t, claims[0].ClaimData["hash_value"])

	// Verify key format.
	data, _ := proto.Marshal(p)
	h := sha256.Sum256(data)
	expectedDigest := hex.EncodeToString(h[:])
	assert.Equal(t, "v0/d/sha256/"+expectedDigest, claims[0].ClaimData["key"])
	assert.Equal(t, expectedDigest, claims[0].ClaimData["hash_value"])
}

func TestStore_EmptyPayloads(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	claims, err := d.Store(storeCtx(), []*commonpb.Payload{})
	require.NoError(t, err)
	assert.Empty(t, claims)
}

func TestStore_Deduplication(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)
	p := testPayload("duplicate-me")

	_, err := d.Store(storeCtx(), []*commonpb.Payload{p})
	require.NoError(t, err)
	assert.Equal(t, int64(1), mc.putCount.Load())

	// Store same payload again — should skip the upload.
	_, err = d.Store(storeCtx(), []*commonpb.Payload{p})
	require.NoError(t, err)
	assert.Equal(t, int64(1), mc.putCount.Load())
}

func TestStore_MultiplePayloads(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	payloads := []*commonpb.Payload{
		testPayload("a"),
		testPayload("b"),
		testPayload("c"),
	}

	claims, err := d.Store(storeCtx(), payloads)
	require.NoError(t, err)
	require.Len(t, claims, 3)

	// Each should have unique keys.
	keys := map[string]bool{}
	for _, c := range claims {
		keys[c.ClaimData["key"]] = true
	}
	assert.Len(t, keys, 3)
}

func TestStore_MaxPayloadSizeExceeded(t *testing.T) {
	mc := newMemClient()
	d, err := NewDriver(Options{
		Client:         mc,
		Bucket:         StaticBucket("b"),
		MaxPayloadSize: 10, // very small
	})
	require.NoError(t, err)

	p := testPayload("this payload is definitely larger than 10 bytes when serialized")
	_, err = d.Store(storeCtx(), []*commonpb.Payload{p})
	assert.ErrorContains(t, err, "s3driver: payload size ")
	assert.ErrorContains(t, err, " exceeds maximum 10")
}

func TestStore_DynamicBucket(t *testing.T) {
	mc := newMemClient()
	d, err := NewDriver(Options{
		Client: mc,
		Bucket: func(_ context.Context, p *commonpb.Payload) string {
			if string(p.Data) == "a" {
				return "bucket-a"
			}
			return "bucket-b"
		},
	})
	require.NoError(t, err)

	claims, err := d.Store(storeCtx(), []*commonpb.Payload{
		testPayload("a"),
		testPayload("b"),
	})
	require.NoError(t, err)
	require.Len(t, claims, 2)

	buckets := map[string]bool{}
	for _, c := range claims {
		buckets[c.ClaimData["bucket"]] = true
	}
	assert.True(t, buckets["bucket-a"])
	assert.True(t, buckets["bucket-b"])
}

// errClient wraps a memClient and injects errors.
type errClient struct {
	*memClient
	putErr error
	getErr error
}

func (e *errClient) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	if e.putErr != nil {
		return e.putErr
	}
	return e.memClient.PutObject(ctx, bucket, key, data)
}

func (e *errClient) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.memClient.GetObject(ctx, bucket, key)
}

func TestStore_PutObjectError(t *testing.T) {
	ec := &errClient{
		memClient: newMemClient(),
		putErr:    errors.New("access denied"),
	}
	d := newDriver(t, ec)

	_, err := d.Store(storeCtx(), []*commonpb.Payload{testPayload("x")})
	assert.ErrorContains(t, err, "s3driver: upload failed [bucket=test-bucket, key=")
	assert.ErrorContains(t, err, "]: access denied")
}

// --- Retrieve tests ---

func TestRetrieve_RoundTrip(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	original := testPayload("round-trip data")
	claims, err := d.Store(storeCtx(), []*commonpb.Payload{original})
	require.NoError(t, err)

	restored, err := d.Retrieve(retrieveCtx(), claims)
	require.NoError(t, err)
	require.Len(t, restored, 1)

	assert.True(t, proto.Equal(original, restored[0]),
		"restored payload should equal original")
}

func TestRetrieve_MultiplePayloads(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	originals := []*commonpb.Payload{
		testPayload("x"),
		testPayload("y"),
		testPayload("z"),
	}
	claims, err := d.Store(storeCtx(), originals)
	require.NoError(t, err)

	restored, err := d.Retrieve(retrieveCtx(), claims)
	require.NoError(t, err)
	require.Len(t, restored, 3)
	for i, orig := range originals {
		assert.True(t, proto.Equal(orig, restored[i]))
	}
}

func TestRetrieve_HashVerificationFailure(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	claims, err := d.Store(storeCtx(), []*commonpb.Payload{testPayload("legit")})
	require.NoError(t, err)

	// Tamper with the stored data.
	for k := range mc.data {
		mc.data[k] = []byte("corrupted")
	}

	_, err = d.Retrieve(retrieveCtx(), claims)
	assert.ErrorContains(t, err, "s3driver: integrity check failed [bucket=test-bucket, key=")
}

func TestRetrieve_UnsupportedHashAlgorithm(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	claims, err := d.Store(storeCtx(), []*commonpb.Payload{testPayload("data")})
	require.NoError(t, err)

	// Override hash algorithm.
	claims[0].ClaimData["hash_algorithm"] = "md5"

	_, err = d.Retrieve(retrieveCtx(), claims)
	assert.EqualError(t, err, `s3driver: unsupported hash algorithm "md5"`)
}

func TestRetrieve_MissingKey(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	claims := []converter.StorageDriverClaim{{
		ClaimData: map[string]string{
			"bucket":         "test-bucket",
			"key":            "v0/d/sha256/nonexistent",
			"hash_algorithm": "sha256",
			"hash_value":     "abc",
		},
	}}

	_, err := d.Retrieve(retrieveCtx(), claims)
	assert.EqualError(t, err, "s3driver: download failed [bucket=test-bucket, key=v0/d/sha256/nonexistent]: not found: test-bucket/v0/d/sha256/nonexistent")
}

func TestRetrieve_GetObjectError(t *testing.T) {
	mc := newMemClient()
	ec := &errClient{memClient: mc, getErr: errors.New("throttled")}
	d := newDriver(t, ec)

	// Store with real client, then retrieve with error client.
	realDriver := newDriver(t, mc)
	claims, err := realDriver.Store(storeCtx(), []*commonpb.Payload{testPayload("data")})
	require.NoError(t, err)

	_, err = d.Retrieve(retrieveCtx(), claims)
	assert.ErrorContains(t, err, "s3driver: download failed [bucket=test-bucket, key=")
	assert.ErrorContains(t, err, "]: throttled")
}

func TestRetrieve_NoHashFields_BackwardCompat(t *testing.T) {
	mc := newMemClient()
	d := newDriver(t, mc)

	// Store normally.
	p := testPayload("compat")
	claims, err := d.Store(storeCtx(), []*commonpb.Payload{p})
	require.NoError(t, err)

	// Strip hash fields to simulate a legacy claim.
	delete(claims[0].ClaimData, "hash_algorithm")
	delete(claims[0].ClaimData, "hash_value")

	restored, err := d.Retrieve(retrieveCtx(), claims)
	require.NoError(t, err)
	assert.True(t, proto.Equal(p, restored[0]))
}

// --- Key generation tests ---

func TestObjectKey(t *testing.T) {
	assert.Equal(t, "v0/d/sha256/abc123", objectKey("abc123"))
}

func TestSha256Hex(t *testing.T) {
	data := []byte("test")
	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])
	assert.Equal(t, expected, sha256Hex(data))
}
