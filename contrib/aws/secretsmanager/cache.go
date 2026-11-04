package secretsmanager

import (
	"context"
	"crypto/tls"
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
	apiKey *cacheEntry
	// client cert and key, fetched and replaced together to prevent (cache-level) tearing on rotations
	clientCert *cacheEntry
	// the CA cert is only be loaded once at client-config-time, but it's here for consistency
	serverCACert *cacheEntry
}

type secretRef struct {
	id         string
	secretType secretType
}

// cacheEntry is one or more secrets that are fetched and replaced together.
type cacheEntry struct {
	// Distinct secret IDs. refs[0].secretType is used as the secret_type metric label.
	refs []secretRef
	// If non-nil, called on newly fetched secrets (in refs order), and an error is treated as a failed fetch
	validate func(secrets []secretValue) error

	// Guards the fields below
	mu sync.Mutex
	// The fetch in progress, or nil if none
	refreshState *refreshState
	// In refs order, nil until the first successful fetch
	secrets   []secretValue
	fetchedAt time.Time
	// Last failed fetch since the last successful fetch, or zero if none
	failedAt time.Time
}

// A fetch in progress. secrets and err are set before done is closed.
type refreshState struct {
	done    chan struct{}
	secrets []secretValue
	err     error
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
		c.apiKey = &cacheEntry{refs: []secretRef{{id: ids.APIKey, secretType: secretTypeAPIKey}}}
	}
	if ids.ClientCert != "" {
		c.clientCert = newClientCertEntry(ids.ClientCert, ids.ClientKey)
	}
	if ids.ServerCACert != "" {
		c.serverCACert = &cacheEntry{refs: []secretRef{{id: ids.ServerCACert, secretType: secretTypeServerCACert}}}
	}
	return c
}

// A newly fetched pair that doesn't match (e.g. fetched partway through rotating separate cert and key secrets) is
// treated as a failed fetch, so the previous pair keeps being used. certID and keyID may be the same if bundled.
func newClientCertEntry(certID, keyID string) *cacheEntry {
	refs := []secretRef{{id: certID, secretType: secretTypeClientCert}}
	if keyID != certID {
		refs = append(refs, secretRef{id: keyID, secretType: secretTypeClientKey})
	}
	validate := func(secrets []secretValue) error {
		if _, err := parseClientCert(secrets); err != nil {
			return &invalidResponseError{fmt.Errorf(
				"failed loading TLS client cert/key from AWS Secrets Manager secrets %q and %q: %w", certID, keyID, err)}
		}
		return nil
	}
	return &cacheEntry{refs: refs, validate: validate}
}

// The get methods must only be called for configured credentials.

func (c *secretCache) getAPIKey(ctx context.Context, tel telemetry) (string, error) {
	secrets, err := c.getEntry(ctx, c.apiKey, tel)
	if err != nil {
		return "", err
	}
	return string(secrets[0].value), nil
}

func (c *secretCache) getClientCert(ctx context.Context, tel telemetry) (*tls.Certificate, error) {
	secrets, err := c.getEntry(ctx, c.clientCert, tel)
	if err != nil {
		return nil, err
	}
	return parseClientCert(secrets)
}

// The returned slice must not be mutated.
func (c *secretCache) getServerCACert(ctx context.Context, tel telemetry) ([]byte, error) {
	secrets, err := c.getEntry(ctx, c.serverCACert, tel)
	if err != nil {
		return nil, err
	}
	return secrets[0].value, nil
}

func parseClientCert(secrets []secretValue) (*tls.Certificate, error) {
	// for a bundled cert/key there's only one value, and X509KeyPair picks out the appropriate PEM blocks
	certPem := secrets[0].value
	keyPem := secrets[len(secrets)-1].value
	cert, err := tls.X509KeyPair(certPem, keyPem)
	return &cert, err
}

// Get the secret value associated with a given cache entry
func (c *secretCache) getEntry(ctx context.Context, e *cacheEntry, tel telemetry) ([]secretValue, error) {
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
	canUseStale := e.secrets != nil && now.Sub(e.fetchedAt) < c.maxStaleness
	stale := e.secrets
	var result lookupResult
	switch {
	case e.secrets != nil && now.Sub(e.fetchedAt) < c.refreshInterval,
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
		r = &refreshState{done: make(chan struct{})}
		e.refreshState = r
		// separate context so the cache's failure state isn't affected by caller context deadlines
		go c.refresh(context.WithoutCancel(ctx), e, r, now, canUseStale, metrics, logger)
	}
	e.mu.Unlock()
	select {
	case <-r.done:
	case <-ctx.Done():
		if canUseStale {
			recordLookup(lookupMissWithFallback)
			return stale, nil
		}
		return nil, ctx.Err()
	}
	if r.err != nil {
		if canUseStale {
			recordLookup(lookupMissWithFallback)
			return stale, nil
		}
		return nil, r.err
	}
	recordLookup(lookupMiss)
	return r.secrets, nil
}

// Fetch an entry's secrets and record the outcome in the cache.
func (c *secretCache) refresh(
	ctx context.Context,
	e *cacheEntry,
	r *refreshState,
	start time.Time,
	canUseStale bool,
	metrics client.MetricsHandler,
	logger log.Logger,
) {
	ctx, cancel := context.WithTimeout(ctx, c.fetchTimeout)
	defer cancel()
	secrets, err := c.fetch(ctx, e)
	latency := c.now().Sub(start)

	// done is closed last so that the fetch's metrics and logs are recorded by the time waiters return
	// (for the unit tests, and also for log ordering in production)
	defer close(r.done)
	e.mu.Lock()
	r.secrets, r.err = secrets, err
	e.refreshState = nil
	prevSecrets, prevFetchedAt, prevFailed := e.secrets, e.fetchedAt, !e.failedAt.IsZero()
	if err != nil {
		e.failedAt = start
	} else {
		e.secrets = secrets
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
		if prevSecrets == nil {
			logger.Info("Fetched AWS Secrets Manager secret",
				"SecretID", ref.id,
				"SecretType", ref.secretType,
				"FetchPath", c.fetcher.path(),
				"VersionID", secrets[i].versionID)
		} else if prevFailed || secrets[i].versionID != prevSecrets[i].versionID {
			logger.Info("Fetched updated AWS Secrets Manager secret",
				"SecretID", ref.id,
				"SecretType", ref.secretType,
				"PreviousVersionID", prevSecrets[i].versionID,
				"VersionID", secrets[i].versionID)
		}
	}
}

// Fetch all of an entry's secrets and validate them.
func (c *secretCache) fetch(ctx context.Context, e *cacheEntry) ([]secretValue, error) {
	secrets := make([]secretValue, 0, len(e.refs))
	for _, ref := range e.refs {
		s, err := c.fetcher.fetch(ctx, ref.id)
		if err != nil {
			return nil, fmt.Errorf("failed fetching AWS Secrets Manager secret %q: %w", ref.id, err)
		}
		secrets = append(secrets, s)
	}
	if e.validate != nil {
		if err := e.validate(secrets); err != nil {
			return nil, err
		}
	}
	return secrets, nil
}
