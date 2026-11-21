package extstore

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"google.golang.org/protobuf/proto"
)

// metricsPayloadCache counts cache hits and rejections on the wrapped cache.
type metricsPayloadCache struct {
	cache             PayloadCache
	hit               metrics.Counter
	hitBytes          metrics.Counter
	rejectionDeclined metrics.Counter
	rejectionError    metrics.Counter
}

func withPayloadCacheMetrics(cache PayloadCache, handler metrics.Handler) PayloadCache {
	return &metricsPayloadCache{
		cache:    cache,
		hit:      handler.Counter(metrics.PayloadCacheLookupHit),
		hitBytes: handler.Counter(metrics.PayloadCacheLookupHitBytes),
		rejectionDeclined: handler.WithTags(map[string]string{
			metrics.ReasonTagName: metrics.PayloadCacheRejectionReasonDeclined,
		}).Counter(metrics.PayloadCacheRejection),
		rejectionError: handler.WithTags(map[string]string{
			metrics.ReasonTagName: metrics.PayloadCacheRejectionReasonError,
		}).Counter(metrics.PayloadCacheRejection),
	}
}

func (c *metricsPayloadCache) Get(ctx context.Context, key string) (*commonpb.Payload, bool, error) {
	payload, found, err := callPayloadCacheGet(ctx, c.cache, key)
	if err == nil && found && payload != nil {
		c.hit.Inc(1)
		c.hitBytes.Inc(int64(proto.Size(payload)))
	}
	return payload, found, err
}

func (c *metricsPayloadCache) Set(ctx context.Context, key string, payload *commonpb.Payload) (bool, error) {
	accepted, err := callPayloadCacheSet(ctx, c.cache, key, payload)
	if err != nil {
		c.rejectionError.Inc(1)
	} else if !accepted {
		c.rejectionDeclined.Inc(1)
	}
	return accepted, err
}
