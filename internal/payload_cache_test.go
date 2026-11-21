package internal

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	"google.golang.org/protobuf/proto"
)

type mapPayloadCache struct {
	mu      sync.Mutex
	entries map[string]*commonpb.Payload
}

func (c *mapPayloadCache) Get(_ context.Context, key string) (*commonpb.Payload, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	payload, ok := c.entries[key]
	return payload, ok, nil
}

func (c *mapPayloadCache) Set(_ context.Context, key string, payload *commonpb.Payload) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = payload
	return true, nil
}

type retrieveCountingStorageDriver struct {
	mu            sync.Mutex
	data          map[string]*commonpb.Payload
	retrieveCount int
}

func (d *retrieveCountingStorageDriver) Name() string { return "counting" }
func (d *retrieveCountingStorageDriver) Type() string { return "counting" }

func (d *retrieveCountingStorageDriver) Store(_ converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageDriverClaim, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	claims := make([]converter.StorageDriverClaim, len(payloads))
	for i, payload := range payloads {
		key := uuid.NewString()
		d.data[key] = payload
		claims[i] = converter.StorageDriverClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}

func (d *retrieveCountingStorageDriver) Retrieve(_ converter.StorageDriverRetrieveContext, claims []converter.StorageDriverClaim) ([]*commonpb.Payload, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retrieveCount++
	payloads := make([]*commonpb.Payload, len(claims))
	for i, claim := range claims {
		payloads[i] = d.data[claim.ClaimData["key"]]
	}
	return payloads, nil
}

func TestResolvePayloadCache(t *testing.T) {
	cache := &mapPayloadCache{entries: map[string]*commonpb.Payload{}}
	require.Nil(t, resolvePayloadCache(nil, false), "unset is off")
	require.Nil(t, resolvePayloadCache(nil, true), "disabled is off")
	require.Same(t, cache, resolvePayloadCache(cache, false))
}

func TestNewClient_RejectsPayloadCacheAndDisablePayloadCache(t *testing.T) {
	options := ClientOptions{
		HostPort:            "localhost:7233",
		PayloadCache:        &mapPayloadCache{entries: map[string]*commonpb.Payload{}},
		DisablePayloadCache: true,
	}

	_, err := NewLazyClient(options)
	require.ErrorContains(t, err, "cannot set both PayloadCache and DisablePayloadCache")
	_, err = DialClient(t.Context(), options)
	require.ErrorContains(t, err, "cannot set both PayloadCache and DisablePayloadCache")
}

// payloadCacheSettingPlugin sets both payload cache options from its NewClient hook.
type payloadCacheSettingPlugin struct {
	ClientPluginBase
}

func (payloadCacheSettingPlugin) Name() string { return "payload-cache-setting" }

func (payloadCacheSettingPlugin) NewClient(
	ctx context.Context,
	options ClientPluginNewClientOptions,
	next func(context.Context, ClientPluginNewClientOptions) error,
) error {
	options.ClientOptions.PayloadCache = &mapPayloadCache{entries: map[string]*commonpb.Payload{}}
	options.ClientOptions.DisablePayloadCache = true
	return next(ctx, options)
}

func TestNewClient_RejectsPayloadCacheAndDisablePayloadCacheSetByPlugin(t *testing.T) {
	_, err := NewLazyClient(ClientOptions{
		HostPort: "localhost:7233",
		Plugins:  []ClientPlugin{payloadCacheSettingPlugin{}},
	})
	require.ErrorContains(t, err, "cannot set both PayloadCache and DisablePayloadCache")
}

func TestNewClientFromExisting_RejectsPayloadCacheAndDisablePayloadCache(t *testing.T) {
	existing, err := NewLazyClient(ClientOptions{HostPort: "localhost:7233"})
	require.NoError(t, err)
	defer existing.Close()

	_, err = NewClientFromExisting(t.Context(), existing, ClientOptions{
		PayloadCache:        &mapPayloadCache{entries: map[string]*commonpb.Payload{}},
		DisablePayloadCache: true,
	})
	require.ErrorContains(t, err, "cannot set both PayloadCache and DisablePayloadCache")
}

func TestWorkerUsesClientPayloadCacheWithMetrics(t *testing.T) {
	cache := &mapPayloadCache{entries: map[string]*commonpb.Payload{}}
	driver := &retrieveCountingStorageDriver{data: map[string]*commonpb.Payload{}}
	handler := metrics.NewCapturingHandler()
	client := NewServiceClient(nil, nil, ClientOptions{
		MetricsHandler:          handler,
		WorkerHeartbeatInterval: -1,
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{driver},
			PayloadSizeThreshold: 10,
		},
		PayloadCache: cache,
	})
	worker := NewAggregatedWorker(client, "payload-cache-queue", WorkerOptions{})
	params := worker.executionParams
	payload := &commonpb.Payload{Data: []byte("a payload above the size threshold")}

	stored, err := params.outboundPayloadVisitor.Visit(&proxy.VisitPayloadsContext{Context: context.Background()}, []*commonpb.Payload{payload})
	require.NoError(t, err)
	require.Len(t, cache.entries, 1, "the worker writes to the client's cache")

	retrieved, err := params.inboundPayloadVisitor.Visit(&proxy.VisitPayloadsContext{Context: context.Background()}, stored)
	require.NoError(t, err)
	require.True(t, proto.Equal(payload, retrieved[0]))
	require.Equal(t, 0, driver.retrieveCount, "the worker reads from the client's cache")

	var hits int64
	for _, counter := range handler.Counters() {
		if counter.Name == metrics.PayloadCacheLookupHit && counter.Tags[metrics.TaskQueueTagName] == "payload-cache-queue" {
			hits += counter.Value()
		}
	}
	require.Equal(t, int64(1), hits)
}
