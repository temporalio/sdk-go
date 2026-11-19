package secretsmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
)

// After a failed refresh falls back to a stale value, wait this long before trying again so that an outage doesn't
// add a fetch attempt to every credential lookup.
const failedRefreshRetryInterval = 30 * time.Second

// Bounds each secret fetch
const defaultFetchTimeout = 10 * time.Second

// secretCache is a TTL cache of the configured credentials that falls back to the last fetched value, up to
// maxStaleness old, when a refresh fails.
type secretCache struct {
	fetcher         fetcher
	refreshInterval time.Duration
	maxStaleness    time.Duration
	fetchTimeout    time.Duration
	now             func() time.Time // normally time.Now, but can be overridden in tests

	// nil if not configured
	apiKey *cacheEntry[string]
	// client cert and key, fetched and replaced together to prevent (cache-level) tearing on rotations
	clientCert *cacheEntry[*tls.Certificate]
	// the CA cert is only loaded once at client-config-time, but it's here for consistency
	serverCACert *cacheEntry[*x509.CertPool]
}

type secretRef struct {
	id         string
	secretType secretType
}

// cacheEntry is one or more secrets that are fetched and replaced together, cached as a single parsed value.
type cacheEntry[T any] struct {
	// Distinct secret IDs. refs[0].secretType is used as the secret_type metric label.
	refs []secretRef
	// Called on newly fetched secrets (in refs order) to produce the cached value. An error is treated as a failed
	// fetch.
	parse func(secrets []secretValue) (T, error)

	// Guards the fields below
	mu sync.Mutex
	// The fetch in progress, or nil if none
	refreshState *refreshState[T]
	// In refs order, nil until the first successful fetch
	versionIDs []string
	value      T
	fetchedAt  time.Time
	// Last failed fetch since the last successful fetch, or zero if none
	failedAt time.Time
}

// A fetch in progress. value and err are set before done is closed.
type refreshState[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func newSecretCache(f fetcher, ids secretIDs, refreshInterval time.Duration, maxStaleness time.Duration) *secretCache {
	c := &secretCache{
		fetcher:         f,
		refreshInterval: refreshInterval,
		maxStaleness:    maxStaleness,
		fetchTimeout:    defaultFetchTimeout,
		now:             time.Now,
	}
	if ids.APIKey != "" {
		c.apiKey = &cacheEntry[string]{
			refs: []secretRef{{id: ids.APIKey, secretType: secretTypeAPIKey}},
			parse: func(secrets []secretValue) (string, error) {
				return string(secrets[0].value), nil
			},
		}
	}
	if ids.ClientCert != "" {
		c.clientCert = newClientCertEntry(ids.ClientCert, ids.ClientKey)
	}
	if ids.ServerCACert != "" {
		c.serverCACert = newServerCACertEntry(ids.ServerCACert)
	}
	return c
}

// A newly fetched pair that doesn't match (e.g. fetched partway through rotating separate cert and key secrets) is
// treated as a failed fetch, so the previous pair keeps being used. certID and keyID may be the same if bundled.
func newClientCertEntry(certID, keyID string) *cacheEntry[*tls.Certificate] {
	refs := []secretRef{{id: certID, secretType: secretTypeClientCert}}
	if keyID != certID {
		refs = append(refs, secretRef{id: keyID, secretType: secretTypeClientKey})
	}
	parse := func(secrets []secretValue) (*tls.Certificate, error) {
		// for a bundled cert/key there's only one value, and X509KeyPair picks out the appropriate PEM blocks
		certPEM := secrets[0].value
		keyPEM := secrets[len(secrets)-1].value
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, &invalidResponseError{fmt.Errorf(
				"failed loading TLS client cert/key from AWS Secrets Manager secrets %q and %q: %w", certID, keyID, err)}
		}
		return &cert, nil
	}
	return &cacheEntry[*tls.Certificate]{refs: refs, parse: parse}
}

func newServerCACertEntry(id string) *cacheEntry[*x509.CertPool] {
	parse := func(secrets []secretValue) (*x509.CertPool, error) {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(secrets[0].value) {
			return nil, &invalidResponseError{fmt.Errorf("no PEM certificates found in TLS server CA cert secret %q", id)}
		}
		return pool, nil
	}
	return &cacheEntry[*x509.CertPool]{
		refs:  []secretRef{{id: id, secretType: secretTypeServerCACert}},
		parse: parse,
	}
}

// The get methods must only be called for configured credentials. Returned values are shared across callers and must
// not be mutated.

func (c *secretCache) getAPIKey(ctx context.Context, tel telemetry) (string, error) {
	return getEntry(ctx, c, c.apiKey, tel)
}

func (c *secretCache) getClientCert(ctx context.Context, tel telemetry) (*tls.Certificate, error) {
	return getEntry(ctx, c, c.clientCert, tel)
}

func (c *secretCache) getServerCACert(ctx context.Context, tel telemetry) (*x509.CertPool, error) {
	return getEntry(ctx, c, c.serverCACert, tel)
}

// Get the parsed value associated with a given cache entry
func getEntry[T any](ctx context.Context, c *secretCache, e *cacheEntry[T], tel telemetry) (T, error) {
	var zero T
	metrics := tel.metrics
	if metrics == nil {
		metrics = client.MetricsNopHandler
	}
	metrics = metrics.WithTags(map[string]string{
		labelSecretType: string(e.refs[0].secretType),
		labelFetchPath:  string(c.fetcher.path()),
	})
	logger := tel.logger
	if logger == nil {
		logger = nopLogger{}
	}
	recordLookup := func(result lookupResult) {
		metrics.WithTags(map[string]string{labelResult: string(result)}).Counter(metricCacheLookup).Inc(1)
	}

	// Check for a usable value in the cache
	e.mu.Lock()
	now := c.now()
	hasValue := e.versionIDs != nil
	canUseStale := hasValue && now.Sub(e.fetchedAt) < c.maxStaleness
	stale := e.value
	var result lookupResult
	switch {
	case hasValue && now.Sub(e.fetchedAt) < c.refreshInterval,
		// Another caller is refreshing, so the current value is still the latest known one
		canUseStale && e.refreshState != nil:
		result = lookupHit
	case canUseStale && now.Sub(e.failedAt) < failedRefreshRetryInterval:
		result = lookupMissWithFallback
	}
	if result != "" {
		e.mu.Unlock()
		recordLookup(result)
		return stale, nil
	}

	// Wait for a fetch, starting one if none is in progress.
	r := e.refreshState
	if r == nil {
		r = &refreshState[T]{done: make(chan struct{})}
		e.refreshState = r
		// separate context so the cache's failure state isn't affected by caller context deadlines
		go refresh(context.WithoutCancel(ctx), c, e, r, now, canUseStale, metrics, logger)
	}
	e.mu.Unlock()
	select {
	case <-r.done:
	case <-ctx.Done():
		if canUseStale {
			recordLookup(lookupMissWithFallback)
			return stale, nil
		}
		return zero, ctx.Err()
	}
	if r.err != nil {
		if canUseStale {
			recordLookup(lookupMissWithFallback)
			return stale, nil
		}
		return zero, r.err
	}
	recordLookup(lookupMiss)
	return r.value, nil
}

// Fetch an entry's secrets and record the outcome in the cache.
func refresh[T any](
	ctx context.Context,
	c *secretCache,
	e *cacheEntry[T],
	r *refreshState[T],
	start time.Time,
	canUseStale bool,
	metrics client.MetricsHandler,
	logger log.Logger,
) {
	ctx, cancel := context.WithTimeout(ctx, c.fetchTimeout)
	defer cancel()
	value, versionIDs, err := fetch(ctx, c, e)
	latency := c.now().Sub(start)

	// done is closed last so that the fetch's metrics and logs are recorded by the time waiters return
	// (for the unit tests, and also for log ordering in production)
	defer close(r.done)
	e.mu.Lock()
	r.value, r.err = value, err
	e.refreshState = nil
	prevVersionIDs, prevFetchedAt, prevFailed := e.versionIDs, e.fetchedAt, !e.failedAt.IsZero()
	if err != nil {
		e.failedAt = start
	} else {
		e.value = value
		e.versionIDs = versionIDs
		e.fetchedAt = start
		e.failedAt = time.Time{}
	}
	e.mu.Unlock()

	// handle telemetry outside the lock
	if err != nil {
		metrics.WithTags(map[string]string{labelOutcome: string(outcomeFailure)}).Timer(metricFetchLatency).Record(latency)
		fb := fallbackNone
		if canUseStale {
			fb = fallbackStale
		}
		metrics.WithTags(map[string]string{
			labelFailureReason: string(classifyFailure(err)),
			labelFallback:      string(fb),
		}).Counter(metricFetchFailed).Inc(1)

		if canUseStale {
			for _, ref := range e.refs {
				logger.Warn("Failed refreshing AWS Secrets Manager secret, using previously fetched value",
					"SecretID", ref.id,
					"SecretType", ref.secretType,
					"Error", err,
					"Age", start.Sub(prevFetchedAt),
					"MaxStaleness", c.maxStaleness)
			}
		}
		return
	}

	metrics.WithTags(map[string]string{labelOutcome: string(outcomeSuccess)}).Timer(metricFetchLatency).Record(latency)
	for i, ref := range e.refs {
		if prevVersionIDs == nil {
			logger.Info("Fetched AWS Secrets Manager secret",
				"SecretID", ref.id,
				"SecretType", ref.secretType,
				"FetchPath", c.fetcher.path(),
				"VersionID", versionIDs[i])
		} else if prevFailed || versionIDs[i] != prevVersionIDs[i] {
			logger.Info("Fetched updated AWS Secrets Manager secret",
				"SecretID", ref.id,
				"SecretType", ref.secretType,
				"PreviousVersionID", prevVersionIDs[i],
				"VersionID", versionIDs[i])
		}
	}
}

// Fetch all of an entry's secrets and parse them. Returns the parsed value and the version IDs in refs order.
func fetch[T any](ctx context.Context, c *secretCache, e *cacheEntry[T]) (T, []string, error) {
	var zero T
	secrets := make([]secretValue, 0, len(e.refs))
	versionIDs := make([]string, 0, len(e.refs))
	for _, ref := range e.refs {
		s, err := c.fetcher.fetch(ctx, ref.id)
		if err != nil {
			return zero, nil, fmt.Errorf("failed fetching AWS Secrets Manager secret %q: %w", ref.id, err)
		}
		secrets = append(secrets, s)
		versionIDs = append(versionIDs, s.versionID)
	}
	value, err := e.parse(secrets)
	if err != nil {
		return zero, nil, err
	}
	return value, versionIDs, nil
}
