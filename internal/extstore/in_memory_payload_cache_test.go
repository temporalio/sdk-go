package extstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

func newTestInMemoryPayloadCache(t *testing.T, options InMemoryPayloadCacheOptions) *InMemoryPayloadCache {
	t.Helper()
	cache, err := NewInMemoryPayloadCache(options)
	require.NoError(t, err)
	return cache
}

func payloadOfSize(t *testing.T, size int) *commonpb.Payload {
	t.Helper()
	// A Payload with only Data serializes to a 1 byte tag, a length, then the bytes.
	payload := &commonpb.Payload{Data: make([]byte, size-2)}
	require.Equal(t, size, proto.Size(payload))
	return payload
}

func requireCached(t *testing.T, cache *InMemoryPayloadCache, key string, expected bool) {
	t.Helper()
	_, found, err := cache.Get(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, expected, found, "key %q", key)
}

func setPayload(t *testing.T, cache *InMemoryPayloadCache, key string, payload *commonpb.Payload) bool {
	t.Helper()
	accepted, err := cache.Set(context.Background(), key, payload)
	require.NoError(t, err)
	return accepted
}

func TestInMemoryPayloadCache_HitAndMiss(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{})
	payload := makePayload(t, "value")

	require.True(t, setPayload(t, cache, "k", payload))

	got, found, err := cache.Get(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, proto.Equal(payload, got))
	requireCached(t, cache, "other", false)
}

func TestInMemoryPayloadCache_ReturnsCopies(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{})
	payload := makePayload(t, "value")
	require.True(t, setPayload(t, cache, "k", payload))

	payload.Data = []byte("changed after set")
	first, _, err := cache.Get(context.Background(), "k")
	require.NoError(t, err)
	first.Data = []byte("changed after get")
	second, _, err := cache.Get(context.Background(), "k")
	require.NoError(t, err)

	require.True(t, proto.Equal(makePayload(t, "value"), second))
}

func TestInMemoryPayloadCache_EvictsLeastRecentlyUsedByEntries(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{MaxEntries: 2})
	setPayload(t, cache, "a", makePayload(t, "a"))
	setPayload(t, cache, "b", makePayload(t, "b"))
	requireCached(t, cache, "a", true)

	setPayload(t, cache, "c", makePayload(t, "c"))

	requireCached(t, cache, "a", true)
	requireCached(t, cache, "b", false)
	requireCached(t, cache, "c", true)
}

func TestInMemoryPayloadCache_EvictsLeastRecentlyUsedByBytes(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{MaxTotalBytes: 100})
	setPayload(t, cache, "a", payloadOfSize(t, 40))
	setPayload(t, cache, "b", payloadOfSize(t, 40))
	requireCached(t, cache, "a", true)

	setPayload(t, cache, "c", payloadOfSize(t, 40))

	requireCached(t, cache, "a", true)
	requireCached(t, cache, "b", false)
	requireCached(t, cache, "c", true)
}

func TestInMemoryPayloadCache_OverwriteReplacesSize(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{MaxTotalBytes: 100})
	setPayload(t, cache, "a", payloadOfSize(t, 60))
	setPayload(t, cache, "b", payloadOfSize(t, 30))

	setPayload(t, cache, "b", payloadOfSize(t, 40))

	requireCached(t, cache, "a", true)
	requireCached(t, cache, "b", true)
}

func TestInMemoryPayloadCache_DeclinesOversizedPayload(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{MaxTotalBytes: 100})
	setPayload(t, cache, "a", payloadOfSize(t, 50))

	require.False(t, setPayload(t, cache, "big", payloadOfSize(t, 101)))

	requireCached(t, cache, "big", false)
	requireCached(t, cache, "a", true)
	require.True(t, setPayload(t, cache, "exact", payloadOfSize(t, 100)))
}

func TestInMemoryPayloadCache_RejectsNegativeLimits(t *testing.T) {
	_, err := NewInMemoryPayloadCache(InMemoryPayloadCacheOptions{MaxTotalBytes: -1})
	require.ErrorContains(t, err, "MaxTotalBytes")
	_, err = NewInMemoryPayloadCache(InMemoryPayloadCacheOptions{MaxEntries: -1})
	require.ErrorContains(t, err, "MaxEntries")
}

func TestInMemoryPayloadCache_ZeroLimitsUseDefaults(t *testing.T) {
	cache := newTestInMemoryPayloadCache(t, InMemoryPayloadCacheOptions{})
	require.Equal(t, 64*1024*1024, cache.maxTotalBytes)
	require.Equal(t, 10_000, cache.maxEntries)
}
