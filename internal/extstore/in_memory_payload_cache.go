package extstore

import (
	"container/list"
	"context"
	"fmt"
	"sync"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

const (
	defaultInMemoryPayloadCacheMaxTotalBytes = 64 * 1024 * 1024
	defaultInMemoryPayloadCacheMaxEntries    = 10_000
)

// InMemoryPayloadCacheOptions are limits for [NewInMemoryPayloadCache].
//
// NOTE: Experimental
type InMemoryPayloadCacheOptions struct {
	// MaxTotalBytes is the total serialized size of all cached payloads. A
	// payload larger than this is not cached.
	//
	// 0 uses the default of 64 MiB. Negative values are rejected.
	MaxTotalBytes int

	// MaxEntries is the maximum number of cached payloads.
	//
	// 0 uses the default of 10,000. Negative values are rejected.
	MaxEntries int
}

// InMemoryPayloadCache is a [PayloadCache] that keeps payloads in process
// memory and evicts the least recently used payload first. It is safe for
// concurrent use.
//
// NOTE: Experimental
type InMemoryPayloadCache struct {
	maxTotalBytes int
	maxEntries    int

	mu           sync.Mutex
	byRecency    *list.List // front is most recently used
	byKey        map[string]*list.Element
	currentBytes int
}

type inMemoryPayloadCacheEntry struct {
	key string
	// Stored serialized so callers cannot change a cached payload.
	encodedPayload []byte
}

var _ PayloadCache = (*InMemoryPayloadCache)(nil)

// NewInMemoryPayloadCache creates an [InMemoryPayloadCache].
//
// NOTE: Experimental
func NewInMemoryPayloadCache(options InMemoryPayloadCacheOptions) (*InMemoryPayloadCache, error) {
	if options.MaxTotalBytes < 0 {
		return nil, fmt.Errorf("InMemoryPayloadCacheOptions.MaxTotalBytes must not be negative, got %d", options.MaxTotalBytes)
	}
	if options.MaxEntries < 0 {
		return nil, fmt.Errorf("InMemoryPayloadCacheOptions.MaxEntries must not be negative, got %d", options.MaxEntries)
	}
	maxTotalBytes := options.MaxTotalBytes
	if maxTotalBytes == 0 {
		maxTotalBytes = defaultInMemoryPayloadCacheMaxTotalBytes
	}
	maxEntries := options.MaxEntries
	if maxEntries == 0 {
		maxEntries = defaultInMemoryPayloadCacheMaxEntries
	}
	return &InMemoryPayloadCache{
		maxTotalBytes: maxTotalBytes,
		maxEntries:    maxEntries,
		byRecency:     list.New(),
		byKey:         map[string]*list.Element{},
	}, nil
}

// Get implements [PayloadCache.Get]. Each call returns a new copy of the payload.
func (c *InMemoryPayloadCache) Get(_ context.Context, key string) (*commonpb.Payload, bool, error) {
	c.mu.Lock()
	element, ok := c.byKey[key]
	if !ok {
		c.mu.Unlock()
		return nil, false, nil
	}
	c.byRecency.MoveToFront(element)
	encodedPayload := element.Value.(*inMemoryPayloadCacheEntry).encodedPayload
	c.mu.Unlock()

	payload := &commonpb.Payload{}
	if err := proto.Unmarshal(encodedPayload, payload); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal cached payload: %w", err)
	}
	return payload, true, nil
}

// Set implements [PayloadCache.Set]. It returns false when the payload is larger
// than MaxTotalBytes.
func (c *InMemoryPayloadCache) Set(_ context.Context, key string, payload *commonpb.Payload) (bool, error) {
	encodedPayload, err := proto.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("failed to marshal payload: %w", err)
	}
	if len(encodedPayload) > c.maxTotalBytes {
		return false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.byKey[key]; ok {
		c.remove(existing)
	}
	for c.byRecency.Len() >= c.maxEntries || c.currentBytes+len(encodedPayload) > c.maxTotalBytes {
		oldest := c.byRecency.Back()
		if oldest == nil {
			break
		}
		c.remove(oldest)
	}
	c.byKey[key] = c.byRecency.PushFront(&inMemoryPayloadCacheEntry{key: key, encodedPayload: encodedPayload})
	c.currentBytes += len(encodedPayload)
	return true, nil
}

// remove must be called with mu held.
func (c *InMemoryPayloadCache) remove(element *list.Element) {
	entry := c.byRecency.Remove(element).(*inMemoryPayloadCacheEntry)
	delete(c.byKey, entry.key)
	c.currentBytes -= len(entry.encodedPayload)
}
