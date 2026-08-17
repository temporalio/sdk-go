package gcsdriver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

const (
	defaultMaxPayloadSize = 50 * 1024 * 1024 // 50 MiB
	driverType            = "gcp.gcsdriver"
	defaultDriverName     = "gcp.gcsdriver"
	hashAlgorithm         = "sha256"
	keyVersion            = "v0"
	nullSegment           = "null"
	maxGCSObjectNameBytes = 1024

	claimKeyBucket        = "bucket"
	claimKeyKey           = "key"
	claimKeyHashAlgorithm = "hash_algorithm"
	claimKeyHashValue     = "hash_value"
)

// BucketFunc resolves the target GCS bucket for a given payload. Use
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

// Options configures the GCS storage driver.
//
// NOTE: Experimental
type Options struct {
	// Client is the GCS client used for storage operations. Required.
	Client Client

	// Bucket resolves the target bucket for each payload. Required.
	// Use StaticBucket("my-bucket") for a fixed bucket.
	Bucket BucketFunc

	// DriverName is a stable, unique identifier for this driver instance.
	// Defaults to "gcp.gcsdriver".
	DriverName string

	// MaxPayloadSize is the maximum serialized payload size in bytes that
	// the driver will accept. Defaults to 50 MiB.
	MaxPayloadSize int
}

// gcsStorageDriver implements converter.StorageDriver by storing payloads in
// Google Cloud Storage using content-addressable keys based on SHA-256 hashes.
type gcsStorageDriver struct {
	client         Client
	bucketFunc     BucketFunc
	driverName     string
	maxPayloadSize int
}

// Compile-time check that gcsStorageDriver implements converter.StorageDriver.
var _ converter.StorageDriver = (*gcsStorageDriver)(nil)

// NewDriver creates a new GCS StorageDriver with the given options.
//
// NOTE: Experimental
func NewDriver(opts Options) (converter.StorageDriver, error) {
	if opts.Client == nil {
		return nil, errors.New("client is required")
	}
	if opts.Bucket == nil {
		return nil, errors.New("bucket is required")
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
	return &gcsStorageDriver{
		client:         opts.Client,
		bucketFunc:     opts.Bucket,
		driverName:     name,
		maxPayloadSize: maxSize,
	}, nil
}

// Name returns the unique identifier for this driver instance.
func (d *gcsStorageDriver) Name() string { return d.driverName }

// Type returns the driver implementation type.
func (d *gcsStorageDriver) Type() string { return driverType }

type preparedPayload struct {
	data      []byte
	hexDigest string
	bucket    string
}

// Store serializes each payload, validates sizes, then uploads concurrently to
// GCS if not already present, and returns a claim per payload.
//
// Two phases are used to avoid partial GCS uploads when validation fails:
//  1. Marshal and validate all payloads sequentially.
//  2. Upload concurrently (bounded to 10) — only reached if all payloads passed validation.
func (d *gcsStorageDriver) Store(
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
	g, gctx := errgroup.WithContext(ctx.Context)
	g.SetLimit(10)
	for i, pp := range prepared {
		g.Go(func() error {
			key := objectKey(ctx.Target, pp.hexDigest)
			exists, err := d.client.ObjectExists(gctx, pp.bucket, key)
			if err != nil {
				return fmt.Errorf("existence check failed [bucket=%s, key=%s%s]: %w", pp.bucket, key, describeClient(d.client), err)
			}
			if !exists {
				if err := d.client.PutObject(gctx, pp.bucket, key, pp.data); err != nil {
					return fmt.Errorf("upload failed [bucket=%s, key=%s%s]: %w", pp.bucket, key, describeClient(d.client), err)
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
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return claims, nil
}

// Retrieve downloads payloads from GCS using the given claims, verifies their
// integrity via SHA-256, and returns the deserialized payloads. Claims are
// processed concurrently.
func (d *gcsStorageDriver) Retrieve(
	ctx converter.StorageDriverRetrieveContext,
	claims []converter.StorageDriverClaim,
) ([]*commonpb.Payload, error) {
	payloads := make([]*commonpb.Payload, len(claims))
	g, gctx := errgroup.WithContext(ctx.Context)
	g.SetLimit(10)

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
				return fmt.Errorf("download failed [bucket=%s, key=%s%s]: %w", bucket, key, describeClient(d.client), err)
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

func objectKey(target converter.StorageDriverTargetInfo, hexDigest string) string {
	digestSegment := "/d/" + hashAlgorithm + "/" + hexDigest
	var key string
	switch t := target.(type) {
	case converter.StorageDriverWorkflowInfo:
		key = keyVersion +
			"/ns/" + encodeObjectNameSegment(t.Namespace) +
			"/wt/" + encodeObjectNameSegment(t.WorkflowType) +
			"/wi/" + encodeObjectNameSegment(t.WorkflowID) +
			"/ri/" + encodeObjectNameSegment(t.RunID) +
			digestSegment
	case converter.StorageDriverActivityInfo:
		key = keyVersion +
			"/ns/" + encodeObjectNameSegment(t.Namespace) +
			"/at/" + encodeObjectNameSegment(t.ActivityType) +
			"/ai/" + encodeObjectNameSegment(t.ActivityID) +
			"/ri/" + encodeObjectNameSegment(t.RunID) +
			digestSegment
	default:
		return keyVersion + digestSegment
	}
	if len(key) > maxGCSObjectNameBytes {
		// Preserve namespace scope for authorization policies.
		var ns string
		switch t := target.(type) {
		case converter.StorageDriverWorkflowInfo:
			ns = t.Namespace
		case converter.StorageDriverActivityInfo:
			ns = t.Namespace
		}
		return keyVersion + "/ns/" + encodeObjectNameSegment(ns) + digestSegment
	}
	return key
}

// describeClient returns ", k=v, k=v" diagnostic info from the client's
// Describe method, or "" if Describe returns nil/empty.
func describeClient(c Client) string {
	m := c.Describe()
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var s string
	for _, k := range keys {
		s += ", " + k + "=" + m[k]
	}
	return s
}

// encodeObjectNameSegment percent-encodes a single path segment for use in a
// GCS object name. GCS object names accept any Unicode, but Google forbids
// carriage-return and line-feed and the literal segments "." and "..", and
// strongly recommends avoiding # [ ] * ? : " < > | and control characters.
// This encodes only that set, plus / (so a value cannot introduce extra path
// segments) and % (so the encoding stays injective), leaving readable Unicode
// intact.
// See https://cloud.google.com/storage/docs/objects#naming.
func encodeObjectNameSegment(s string) string {
	if s == "" {
		return nullSegment
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isGCSUnsafeRune(r) {
			// Percent-encode each byte of the rune's UTF-8 representation.
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], r)
			for _, c := range buf[:n] {
				b.WriteByte('%')
				b.WriteByte(upperHexDigit(c >> 4))
				b.WriteByte(upperHexDigit(c & 0xf))
			}
		} else {
			b.WriteRune(r)
		}
	}
	encoded := b.String()

	// The literal segments "." and ".." are forbidden by GCS.
	if encoded == "." {
		return "%2E"
	}
	if encoded == ".." {
		return "%2E%2E"
	}
	return encoded
}

// isGCSUnsafeRune reports whether the rune should be percent-encoded in a GCS
// object name segment. The unsafe set includes Unicode control characters
// (C0: U+0000–U+001F, DEL: U+007F, C1: U+0080–U+009F), the ASCII characters
// Google recommends against (# [ ] * ? : " < > |), forward slash (prevents
// path injection), and percent (keeps encoding injective).
func isGCSUnsafeRune(r rune) bool {
	switch {
	case r <= 0x1f: // C0 control characters
		return true
	case r >= 0x7f && r <= 0x9f: // DEL + C1 control characters
		return true
	default:
		return r < 0x80 && strings.IndexByte(gcsUnsafeChars, byte(r)) >= 0
	}
}

// gcsUnsafeChars is the set of non-control ASCII characters that must be
// percent-encoded in GCS object name segments.
const gcsUnsafeChars = "#[]*?:\"<>|/%"

func upperHexDigit(n byte) byte {
	return "0123456789ABCDEF"[n]
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
