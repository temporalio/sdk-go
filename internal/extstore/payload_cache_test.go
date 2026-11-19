package extstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"google.golang.org/protobuf/proto"
)

// recordingPayloadCache is a PayloadCache for tests. It can fail or panic on demand.
type recordingPayloadCache struct {
	mu         sync.Mutex
	entries    map[string]*commonpb.Payload
	getCount   int
	setCount   int
	decline    bool
	getErr     error
	setErr     error
	panicOnGet bool
	panicOnSet bool
}

func newRecordingPayloadCache() *recordingPayloadCache {
	return &recordingPayloadCache{entries: map[string]*commonpb.Payload{}}
}

func (c *recordingPayloadCache) Get(_ context.Context, key string) (*commonpb.Payload, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCount++
	if c.panicOnGet {
		panic("get panic")
	}
	if c.getErr != nil {
		return nil, false, c.getErr
	}
	payload, ok := c.entries[key]
	return payload, ok, nil
}

func (c *recordingPayloadCache) Set(_ context.Context, key string, payload *commonpb.Payload) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCount++
	if c.panicOnSet {
		panic("set panic")
	}
	if c.setErr != nil {
		return false, c.setErr
	}
	if c.decline {
		return false, nil
	}
	c.entries[key] = payload
	return true, nil
}

func (c *recordingPayloadCache) keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	return keys
}

func cachedStorageParams(t *testing.T, cache PayloadCache, drivers ...StorageDriver) StorageParameters {
	t.Helper()
	params, err := ExternalStorageToParams(ExternalStorage{
		Drivers:              drivers,
		DriverSelector:       singleDriverSelector{driver: drivers[0]},
		PayloadSizeThreshold: 10,
	})
	require.NoError(t, err)
	return params.WithPayloadCache(cache)
}

func storeOne(t *testing.T, params StorageParameters, payload *commonpb.Payload) *commonpb.Payload {
	t.Helper()
	stored, err := visitPayloads(context.Background(), NewExternalStorageVisitor(params), []*commonpb.Payload{payload})
	require.NoError(t, err)
	require.True(t, IsStorageReference(stored[0]))
	return stored[0]
}

// ---------------------------------------------------------------------------
// claimCacheKey
// ---------------------------------------------------------------------------

// These expected keys are shared with the other SDKs and must not change.
func TestClaimCacheKey_MatchesOtherSDKs(t *testing.T) {
	require.Equal(t, "v0/temporal/extstore/6:aws-s36:bucket3:abc3:key1:1",
		claimCacheKey("aws-s3", map[string]string{"key": "1", "bucket": "abc"}))
	require.Equal(t, "v0/temporal/extstore/1:d1:k2:é",
		claimCacheKey("d", map[string]string{"k": "é"}))
	// U+FF01 sorts before U+1D11E by UTF-8 bytes, but after it by UTF-16 code units.
	require.Equal(t, "v0/temporal/extstore/1:d3:！1:a4:𝄞1:b",
		claimCacheKey("d", map[string]string{"\U0001D11E": "b", "！": "a"}))
}

func TestClaimCacheKey_Distinguishes(t *testing.T) {
	base := claimCacheKey("s3", map[string]string{"bucket": "abc", "key": "1"})
	require.NotEqual(t, base, claimCacheKey("gcs", map[string]string{"bucket": "abc", "key": "1"}))
	require.NotEqual(t, base, claimCacheKey("s3", map[string]string{"bucket": "abc", "key": "2"}))
	require.NotEqual(t,
		claimCacheKey("s3", map[string]string{"a": "1:b1:2"}),
		claimCacheKey("s3", map[string]string{"a": "1", "b": "2"}))
}

// ---------------------------------------------------------------------------
// Visitors with a payload cache
// ---------------------------------------------------------------------------

func TestStoreVisitor_WritesStoredPayloadToCache(t *testing.T) {
	driver := newTestDriver("d")
	cache := newRecordingPayloadCache()
	params := cachedStorageParams(t, cache, driver)
	payload := makeOversizedPayload(t, 10)

	ref := storeOne(t, params, payload)

	decoded, err := payloadToStorageReference(ref)
	require.NoError(t, err)
	cachedPayload, found, err := cache.Get(context.Background(), claimCacheKey("d", decoded.ClaimData))
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, proto.Equal(payload, cachedPayload))
}

func TestRetrievalVisitor_CacheHitSkipsDriver(t *testing.T) {
	driver := newTestDriver("d")
	cache := newRecordingPayloadCache()
	params := cachedStorageParams(t, cache, driver)
	payload := makeOversizedPayload(t, 10)
	ref := storeOne(t, params, payload)

	retrieved, err := visitPayloads(context.Background(), NewExternalRetrievalVisitor(params), []*commonpb.Payload{ref})

	require.NoError(t, err)
	require.True(t, proto.Equal(payload, retrieved[0]))
	require.Equal(t, 0, driver.retrieveCount)
}

func TestRetrievalVisitor_CacheMissDownloadsAndFillsCache(t *testing.T) {
	driver := newTestDriver("d")
	payload := makeOversizedPayload(t, 10)
	ref := storeOne(t, cachedStorageParams(t, nil, driver), payload)
	cache := newRecordingPayloadCache()
	params := cachedStorageParams(t, cache, driver)

	retrieved, err := visitPayloads(context.Background(), NewExternalRetrievalVisitor(params), []*commonpb.Payload{ref})
	require.NoError(t, err)
	require.True(t, proto.Equal(payload, retrieved[0]))
	require.Equal(t, 1, driver.retrieveCount)
	require.Len(t, cache.keys(), 1)

	retrieved, err = visitPayloads(context.Background(), NewExternalRetrievalVisitor(params), []*commonpb.Payload{ref})
	require.NoError(t, err)
	require.True(t, proto.Equal(payload, retrieved[0]))
	require.Equal(t, 1, driver.retrieveCount)
}

// recordingRetrieveDriver records the claims it is asked to retrieve.
type recordingRetrieveDriver struct {
	*testStorageDriver
	retrievedClaims []StorageDriverClaim
}

func (d *recordingRetrieveDriver) Retrieve(ctx StorageDriverRetrieveContext, claims []StorageDriverClaim) ([]*commonpb.Payload, error) {
	d.retrievedClaims = append(d.retrievedClaims, claims...)
	return d.testStorageDriver.Retrieve(ctx, claims)
}

func TestRetrievalVisitor_DownloadsOnlyMisses(t *testing.T) {
	driver := &recordingRetrieveDriver{testStorageDriver: newTestDriver("d")}
	uncachedParams := cachedStorageParams(t, nil, driver)
	cache := newRecordingPayloadCache()
	cachedParams := cachedStorageParams(t, cache, driver)
	hitPayload := makeOversizedPayload(t, 10)
	missPayload := makeOversizedPayload(t, 20)
	hitRef := storeOne(t, cachedParams, hitPayload)
	missRef := storeOne(t, uncachedParams, missPayload)
	inline := makePayload(t, "x")

	retrieved, err := visitPayloads(context.Background(), NewExternalRetrievalVisitor(cachedParams),
		[]*commonpb.Payload{missRef, inline, hitRef})

	require.NoError(t, err)
	require.True(t, proto.Equal(missPayload, retrieved[0]))
	require.Same(t, inline, retrieved[1])
	require.True(t, proto.Equal(hitPayload, retrieved[2]))
	missClaim, err := payloadToStorageReference(missRef)
	require.NoError(t, err)
	require.Equal(t, []StorageDriverClaim{{ClaimData: missClaim.ClaimData}}, driver.retrievedClaims)
}

func TestVisitors_FailingCacheNeverFailsOperation(t *testing.T) {
	for name, cache := range map[string]*recordingPayloadCache{
		"errors": {entries: map[string]*commonpb.Payload{}, getErr: errors.New("get failed"), setErr: errors.New("set failed")},
		"panics": {entries: map[string]*commonpb.Payload{}, panicOnGet: true, panicOnSet: true},
	} {
		t.Run(name, func(t *testing.T) {
			driver := newTestDriver("d")
			params := cachedStorageParams(t, cache, driver)
			payload := makeOversizedPayload(t, 10)

			ref := storeOne(t, params, payload)
			retrieved, err := visitPayloads(context.Background(), NewExternalRetrievalVisitor(params), []*commonpb.Payload{ref})

			require.NoError(t, err)
			require.True(t, proto.Equal(payload, retrieved[0]))
			require.Equal(t, 1, driver.retrieveCount)
			require.Equal(t, 1, cache.getCount)
			require.Equal(t, 2, cache.setCount)
		})
	}
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

func counterValue(t *testing.T, handler *metrics.CapturingHandler, name string, tags map[string]string) int64 {
	t.Helper()
	for _, counter := range handler.Counters() {
		if counter.Name != name {
			continue
		}
		matches := true
		for key, value := range tags {
			if counter.Tags[key] != value {
				matches = false
			}
		}
		if matches {
			return counter.Value()
		}
	}
	return 0
}

func TestPayloadCacheMetrics(t *testing.T) {
	ctx := context.Background()
	handler := metrics.NewCapturingHandler()
	inner := newRecordingPayloadCache()
	cache := withPayloadCacheMetrics(inner, handler.WithTags(map[string]string{metrics.TaskQueueTagName: "tq"}))
	payload := makeOversizedPayload(t, 10)

	accepted, err := cache.Set(ctx, "k", payload)
	require.NoError(t, err)
	require.True(t, accepted)
	_, found, err := cache.Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, found)
	_, found, err = cache.Get(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)

	inner.decline = true
	accepted, err = cache.Set(ctx, "declined", payload)
	require.NoError(t, err)
	require.False(t, accepted)

	inner.decline = false
	inner.setErr = errors.New("set failed")
	_, err = cache.Set(ctx, "error", payload)
	require.Error(t, err)

	inner.setErr = nil
	inner.panicOnSet = true
	_, err = cache.Set(ctx, "panic", payload)
	require.Error(t, err)

	queueTag := map[string]string{metrics.TaskQueueTagName: "tq"}
	require.Equal(t, int64(1), counterValue(t, handler, metrics.PayloadCacheLookupHit, queueTag))
	require.Equal(t, int64(proto.Size(payload)), counterValue(t, handler, metrics.PayloadCacheLookupHitBytes, queueTag))
	require.Equal(t, int64(1), counterValue(t, handler, metrics.PayloadCacheRejection,
		map[string]string{metrics.TaskQueueTagName: "tq", metrics.ReasonTagName: metrics.PayloadCacheRejectionReasonDeclined}))
	require.Equal(t, int64(2), counterValue(t, handler, metrics.PayloadCacheRejection,
		map[string]string{metrics.TaskQueueTagName: "tq", metrics.ReasonTagName: metrics.PayloadCacheRejectionReasonError}))
}

func TestWithPayloadCacheMetrics_NoCacheStaysOff(t *testing.T) {
	params := StorageParameters{}.WithPayloadCacheMetrics(metrics.NewCapturingHandler())
	require.Nil(t, params.payloadCache)
}
