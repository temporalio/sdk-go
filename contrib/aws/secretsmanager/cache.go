package secretsmanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
)

// After a failed refresh falls back to a stale value, wait this long before trying again so that an outage doesn't
// add a fetch attempt to every credential lookup.
const failedRefreshRetryInterval = 30 * time.Second

// secretCache is a TTL cache of secret values that falls back to the last fetched value, up to maxStaleness old, when
// a refresh fails.
type secretCache struct {
	fetcher         fetcher
	refreshInterval time.Duration
	maxStaleness    time.Duration
	now             func() time.Time // normally time.Now, but can be overridden in tests

	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	// Held during fetch so concurrent lookups of the same secret share one fetch
	mu        sync.Mutex
	value     []byte
	versionID string
	fetchedAt time.Time
	// Last failed fetch since the last successful fetch, or zero if none
	failedAt time.Time
}

func newSecretCache(f fetcher, refreshInterval time.Duration, maxStaleness time.Duration) *secretCache {
	return &secretCache{
		fetcher:         f,
		refreshInterval: refreshInterval,
		maxStaleness:    maxStaleness,
		now:             time.Now,
		entries:         map[string]*cacheEntry{},
	}
}

// get returns the secret's value. The returned slice must not be mutated. secretType is the secret_type label for
// logs and metrics.
func (c *secretCache) get(ctx context.Context, secretID string, secretType secretType, tel telemetry) ([]byte, error) {
	c.mu.Lock()
	e := c.entries[secretID]
	if e == nil {
		e = &cacheEntry{}
		c.entries[secretID] = e
	}
	c.mu.Unlock()

	metrics := tel.metrics
	if metrics == nil {
		metrics = client.MetricsNopHandler
	}
	metrics = metrics.WithTags(map[string]string{labelSecretType: string(secretType), labelFetchPath: string(c.fetcher.path())})
	logger := tel.logger
	if logger == nil {
		logger = nopLogger{}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	now := c.now()
	if e.value != nil && now.Sub(e.fetchedAt) < c.refreshInterval {
		metrics.WithTags(map[string]string{labelResult: string(lookupHit)}).Counter(metricCacheLookup).Inc(1)
		return e.value, nil
	}
	canUseStale := e.value != nil && now.Sub(e.fetchedAt) < c.maxStaleness
	if canUseStale && now.Sub(e.failedAt) < failedRefreshRetryInterval {
		metrics.WithTags(map[string]string{labelResult: string(lookupMissWithFallback)}).Counter(metricCacheLookup).Inc(1)
		return e.value, nil
	}

	s, err := c.fetcher.fetch(ctx, secretID)
	latency := c.now().Sub(now)
	if err != nil {
		e.failedAt = now

		metrics.WithTags(map[string]string{labelOutcome: string(outcomeFailure)}).Timer(metricFetchLatency).Record(latency)
		fb := fallbackNone
		if canUseStale {
			fb = fallbackStale
		}
		metrics.WithTags(map[string]string{
			labelFailureReason: string(classifyFailure(err)),
			labelFallback:      string(fb),
		}).Counter(metricFetchFailed).Inc(1)

		if !canUseStale {
			return nil, fmt.Errorf("failed fetching AWS Secrets Manager secret %q: %w", secretID, err)
		}
		logger.Warn("Failed refreshing AWS Secrets Manager secret, using previously fetched value",
			"SecretID", secretID,
			"SecretType", secretType,
			"Error", err,
			"Age", now.Sub(e.fetchedAt),
			"MaxStaleness", c.maxStaleness)
		metrics.WithTags(map[string]string{labelResult: string(lookupMissWithFallback)}).Counter(metricCacheLookup).Inc(1)
		return e.value, nil
	}

	metrics.WithTags(map[string]string{labelOutcome: string(outcomeSuccess)}).Timer(metricFetchLatency).Record(latency)
	metrics.WithTags(map[string]string{labelResult: string(lookupMiss)}).Counter(metricCacheLookup).Inc(1)
	if e.value == nil {
		logger.Info("Fetched AWS Secrets Manager secret",
			"SecretID", secretID,
			"SecretType", secretType,
			"FetchPath", c.fetcher.path(),
			"VersionID", s.versionID)
	} else if !e.failedAt.IsZero() || s.versionID != e.versionID {
		logger.Info("Fetched updated AWS Secrets Manager secret",
			"SecretID", secretID,
			"SecretType", secretType,
			"PreviousVersionID", e.versionID,
			"VersionID", s.versionID)
	}

	e.value = s.value
	e.versionID = s.versionID
	e.fetchedAt = now
	e.failedAt = time.Time{}
	return e.value, nil
}
