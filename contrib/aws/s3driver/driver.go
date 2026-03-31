package s3driver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

const (
	defaultMaxPayloadSize = 50 * 1024 * 1024 // 50 MiB
	driverType            = "aws.s3driver"
	defaultDriverName     = "aws.s3driver"
	hashAlgorithm         = "sha256"
	keyVersion            = "v0"

	claimKeyBucket        = "bucket"
	claimKeyKey           = "key"
	claimKeyHashAlgorithm = "hash_algorithm"
	claimKeyHashValue     = "hash_value"
)

// BucketFunc resolves the target S3 bucket for a given payload. Use
// StaticBucket for a fixed bucket name.
//
// NOTE: Experimental
type BucketFunc func(ctx converter.StorageDriverStoreContext, payload *commonpb.Payload) string

// StaticBucket returns a BucketFunc that always returns the given bucket name.
//
// NOTE: Experimental
func StaticBucket(name string) BucketFunc {
	return func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) string { return name }
}

// Options configures the S3 storage driver.
//
// NOTE: Experimental
type Options struct {
	// Client is the S3 client used for storage operations. Required.
	Client Client

	// Bucket resolves the target bucket for each payload. Required.
	// Use StaticBucket("my-bucket") for a fixed bucket.
	Bucket BucketFunc

	// DriverName is a stable, unique identifier for this driver instance.
	// Defaults to "aws.s3driver".
	DriverName string

	// MaxPayloadSize is the maximum serialized payload size in bytes that
	// the driver will accept. Defaults to 50 MiB.
	MaxPayloadSize int
}

// s3StorageDriver implements converter.StorageDriver by storing payloads in
// Amazon S3 using content-addressable keys based on SHA-256 hashes.
type s3StorageDriver struct {
	client         Client
	bucketFunc     BucketFunc
	driverName     string
	maxPayloadSize int
}

// Compile-time check that s3StorageDriver implements converter.StorageDriver.
var _ converter.StorageDriver = (*s3StorageDriver)(nil)

// NewDriver creates a new S3 StorageDriver with the given options.
//
// NOTE: Experimental
func NewDriver(opts Options) (converter.StorageDriver, error) {
	if opts.Client == nil {
		return nil, errors.New("Client is required")
	}
	if opts.Bucket == nil {
		return nil, errors.New("Bucket is required")
	}
	name := opts.DriverName
	if name == "" {
		name = defaultDriverName
	}
	maxSize := opts.MaxPayloadSize
	if maxSize == 0 {
		maxSize = defaultMaxPayloadSize
	}
	if maxSize < 0 {
		return nil, fmt.Errorf("MaxPayloadSize must be positive, got %d", maxSize)
	}
	return &s3StorageDriver{
		client:         opts.Client,
		bucketFunc:     opts.Bucket,
		driverName:     name,
		maxPayloadSize: maxSize,
	}, nil
}

// Name returns the unique identifier for this driver instance.
func (d *s3StorageDriver) Name() string { return d.driverName }

// Type returns the driver implementation type.
func (d *s3StorageDriver) Type() string { return driverType }

type preparedPayload struct {
	data      []byte
	hexDigest string
	bucket    string
}

// Store serializes each payload, validates sizes, then uploads concurrently to
// S3 if not already present, and returns a claim per payload.
//
// Two phases are used to avoid partial S3 uploads when validation fails:
//  1. Marshal and validate all payloads sequentially.
//  2. Upload concurrently — only reached if all payloads passed validation.
func (d *s3StorageDriver) Store(
	ctx converter.StorageDriverStoreContext,
	payloads []*commonpb.Payload,
) ([]converter.StorageDriverClaim, error) {
	prepared := make([]preparedPayload, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		if len(data) > d.maxPayloadSize {
			return nil, fmt.Errorf(
				"payload size %d exceeds maximum %d",
				len(data), d.maxPayloadSize,
			)
		}
		prepared[i] = preparedPayload{
			data:      data,
			hexDigest: sha256Hex(data),
			bucket:    d.bucketFunc(ctx, p),
		}
	}

	claims := make([]converter.StorageDriverClaim, len(payloads))
	eg2, egCtx := errgroup.WithContext(ctx.Context)
	for i, pp := range prepared {
		eg2.Go(func() error {
			key := objectKey(pp.hexDigest)
			exists, err := d.client.ObjectExists(egCtx, pp.bucket, key)
			if err != nil {
				return fmt.Errorf("existence check failed [bucket=%s, key=%s]: %w", pp.bucket, key, err)
			}
			if !exists {
				if err := d.client.PutObject(egCtx, pp.bucket, key, pp.data); err != nil {
					return fmt.Errorf("upload failed [bucket=%s, key=%s]: %w", pp.bucket, key, err)
				}
			}
			claims[i] = converter.StorageDriverClaim{
				ClaimData: map[string]string{
					claimKeyBucket:        pp.bucket,
					claimKeyKey:           key,
					claimKeyHashAlgorithm: hashAlgorithm,
					claimKeyHashValue:     pp.hexDigest,
				},
			}
			return nil
		})
	}
	if err := eg2.Wait(); err != nil {
		return nil, err
	}
	return claims, nil
}

// Retrieve downloads payloads from S3 using the given claims, verifies their
// integrity via SHA-256, and returns the deserialized payloads. Claims are
// processed concurrently.
func (d *s3StorageDriver) Retrieve(
	ctx converter.StorageDriverRetrieveContext,
	claims []converter.StorageDriverClaim,
) ([]*commonpb.Payload, error) {
	payloads := make([]*commonpb.Payload, len(claims))
	g, gctx := errgroup.WithContext(ctx.Context)

	for i, c := range claims {
		g.Go(func() error {
			bucket, ok := c.ClaimData[claimKeyBucket]
			if !ok {
				return fmt.Errorf("claim missing field %q", claimKeyBucket)
			}
			key, ok := c.ClaimData[claimKeyKey]
			if !ok {
				return fmt.Errorf("claim missing field %q", claimKeyKey)
			}

			data, err := d.client.GetObject(gctx, bucket, key)
			if err != nil {
				return fmt.Errorf("download failed [bucket=%s, key=%s]: %w", bucket, key, err)
			}

			algo, ok := c.ClaimData[claimKeyHashAlgorithm]
			if !ok {
				return fmt.Errorf("claim missing field %q", claimKeyHashAlgorithm)
			}
			if algo != hashAlgorithm {
				return fmt.Errorf("unsupported hash algorithm %q", algo)
			}

			expectedHash, ok := c.ClaimData[claimKeyHashValue]
			if !ok {
				return fmt.Errorf("claim missing field %q", claimKeyHashValue)
			}
			if actualHash := sha256Hex(data); actualHash != expectedHash {
				return fmt.Errorf(
					"integrity check failed [bucket=%s, key=%s]: expected hash %s, got %s",
					bucket, key, expectedHash, actualHash,
				)
			}

			var payload commonpb.Payload
			if err := proto.Unmarshal(data, &payload); err != nil {
				return fmt.Errorf("failed to unmarshal payload [bucket=%s, key=%s]: %w", bucket, key, err)
			}
			payloads[i] = &payload
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return payloads, nil
}

func objectKey(hexDigest string) string {
	return keyVersion + "/d/" + hashAlgorithm + "/" + hexDigest
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
