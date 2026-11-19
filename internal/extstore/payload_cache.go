package extstore

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	commonpb "go.temporal.io/api/common/v1"
)

// PayloadCache stores payloads under opaque string keys. The SDK uses it to
// avoid downloading the same externally stored payload more than once.
//
// Implementations must be safe for concurrent use. They should not panic. The
// SDK treats a failed Get as a miss and ignores a failed Set.
//
// NOTE: Experimental
type PayloadCache interface {
	// Get returns the payload stored under key. found is false on a miss.
	Get(ctx context.Context, key string) (payload *commonpb.Payload, found bool, err error)

	// Set stores payload under key and reports whether the cache accepted it.
	Set(ctx context.Context, key string, payload *commonpb.Payload) (accepted bool, err error)
}

// claimCacheKeyPrefix and the encoding in claimCacheKey must stay identical
// across SDKs so that a cache shared between them finds the same entries.
const claimCacheKeyPrefix = "v0/temporal/extstore/"

// claimCacheKey returns the cache key for a stored payload. Claim entries are
// ordered by the bytes of their keys, so the result does not depend on map order.
func claimCacheKey(driverName string, claimData map[string]string) string {
	keys := make([]string, 0, len(claimData))
	for key := range claimData {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	var builder strings.Builder
	builder.WriteString(claimCacheKeyPrefix)
	writeLengthPrefixed(&builder, driverName)
	for _, key := range keys {
		writeLengthPrefixed(&builder, key)
		writeLengthPrefixed(&builder, claimData[key])
	}
	return builder.String()
}

func writeLengthPrefixed(builder *strings.Builder, value string) {
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
}

func callPayloadCacheGet(ctx context.Context, cache PayloadCache, key string) (payload *commonpb.Payload, found bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			payload, found, err = nil, false, fmt.Errorf("panicked: %v", r)
		}
	}()
	return cache.Get(ctx, key)
}

func callPayloadCacheSet(ctx context.Context, cache PayloadCache, key string, payload *commonpb.Payload) (accepted bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			accepted, err = false, fmt.Errorf("panicked: %v", r)
		}
	}()
	return cache.Set(ctx, key, payload)
}

// cacheGet looks up key, treating a failure as a miss.
func (p StorageParameters) cacheGet(ctx context.Context, key string) (*commonpb.Payload, bool) {
	if p.payloadCache == nil {
		return nil, false
	}
	payload, found, err := callPayloadCacheGet(ctx, p.payloadCache, key)
	if err != nil || !found || payload == nil {
		return nil, false
	}
	return payload, true
}

// cacheSet stores payload under key, ignoring a failure.
func (p StorageParameters) cacheSet(ctx context.Context, key string, payload *commonpb.Payload) {
	if p.payloadCache == nil {
		return
	}
	_, _ = callPayloadCacheSet(ctx, p.payloadCache, key, payload)
}
