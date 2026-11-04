package secretsmanager

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type funcFetcher func(ctx context.Context, secretID string) (secretValue, error)

func (f funcFetcher) fetch(ctx context.Context, secretID string) (secretValue, error) {
	return f(ctx, secretID)
}
func (funcFetcher) path() fetchPath { return fetchPathSDK }

func TestSecretCache(t *testing.T) {
	var (
		value    = "v1"
		version  = "ver1"
		fetchErr error
		fetches  int
		now      = time.Unix(1000, 0)
	)
	c := newSecretCache(funcFetcher(func(context.Context, string) (secretValue, error) {
		fetches++
		now = now.Add(time.Millisecond) // fetch latency
		if fetchErr != nil {
			return secretValue{}, fetchErr
		}
		return secretValue{value: []byte(value), versionID: version}, nil
	}), secretIDs{APIKey: "id"}, time.Minute, time.Hour)
	c.now = func() time.Time { return now }
	logger := &recordingLogger{}
	metrics := newRecordingMetrics()
	tel := telemetry{logger: logger, metrics: metrics}
	get := func() (string, error) {
		return c.getAPIKey(context.Background(), tel)
	}
	lookups := func(result lookupResult) int64 {
		return metrics.counter(metricCacheLookup,
			labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathSDK, labelResult, result)
	}
	failures := func(fallback fallback) int64 {
		return metrics.counter(metricFetchFailed, labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathSDK,
			labelFailureReason, failureOther, labelFallback, fallback)
	}
	latencies := func(outcome fetchOutcome) []time.Duration {
		return metrics.timer(metricFetchLatency,
			labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathSDK, labelOutcome, outcome)
	}

	// Initial fetch, then cached until refresh interval
	v, err := get()
	require.NoError(t, err)
	require.Equal(t, "v1", v)
	require.Equal(t, []string{
		"INFO Fetched AWS Secrets Manager secret SecretID=id SecretType=api_key FetchPath=sdk VersionID=ver1",
	}, logger.take())
	require.Equal(t, int64(1), lookups(lookupMiss))
	require.Equal(t, []time.Duration{time.Millisecond}, latencies(outcomeSuccess))
	value = "v2"
	now = now.Add(59 * time.Second)
	v, err = get()
	require.NoError(t, err)
	require.Equal(t, "v1", v)
	require.Equal(t, 1, fetches)
	require.Equal(t, int64(1), lookups(lookupHit))

	// Refreshed after interval. Same version isn't logged as a rotation.
	now = now.Add(time.Second)
	v, err = get()
	require.NoError(t, err)
	require.Equal(t, "v2", v)
	require.Equal(t, 2, fetches)
	require.Empty(t, logger.take())
	require.Equal(t, int64(2), lookups(lookupMiss))

	// New version is logged as a rotation
	version = "ver2"
	now = now.Add(time.Minute)
	_, err = get()
	require.NoError(t, err)
	require.Equal(t, []string{
		"INFO Fetched updated AWS Secrets Manager secret SecretID=id SecretType=api_key PreviousVersionID=ver1 " +
			"VersionID=ver2",
	}, logger.take())

	// Failed refresh falls back to stale value, and doesn't retry until the retry interval passes
	fetchErr = errors.New("outage")
	now = now.Add(time.Minute)
	v, err = get()
	require.NoError(t, err)
	require.Equal(t, "v2", v)
	require.Equal(t, 4, fetches)
	require.Equal(t, []string{
		"WARN Failed refreshing AWS Secrets Manager secret, using previously fetched value SecretID=id " +
			"SecretType=api_key Error=failed fetching AWS Secrets Manager secret \"id\": outage Age=1m0.001s MaxStaleness=1h0m0s",
	}, logger.take())
	require.Equal(t, int64(1), failures(fallbackStale))
	require.Equal(t, int64(1), lookups(lookupMissWithFallback))
	require.Equal(t, []time.Duration{time.Millisecond}, latencies(outcomeFailure))
	now = now.Add(failedRefreshRetryInterval - time.Second)
	v, err = get()
	require.NoError(t, err)
	require.Equal(t, "v2", v)
	require.Equal(t, 4, fetches)
	require.Empty(t, logger.take())
	require.Equal(t, int64(2), lookups(lookupMissWithFallback))
	now = now.Add(time.Second)
	_, err = get()
	require.NoError(t, err)
	require.Equal(t, 5, fetches)
	require.Len(t, logger.take(), 1)
	require.Equal(t, int64(2), failures(fallbackStale))

	// Nil logger and metrics handler are allowed
	now = now.Add(failedRefreshRetryInterval)
	_, err = c.getAPIKey(context.Background(), telemetry{})
	require.NoError(t, err)
	require.Equal(t, 6, fetches)
	require.Empty(t, logger.take())

	// Fails once past max staleness
	now = now.Add(time.Hour)
	_, err = get()
	require.ErrorContains(t, err, `failed fetching AWS Secrets Manager secret "id": outage`)
	require.Empty(t, logger.take())
	require.Equal(t, int64(1), failures(fallbackNone))
	require.Equal(t, int64(3), lookups(lookupMissWithFallback))

	// Recovers. Logged even though the version is unchanged.
	fetchErr = nil
	value = "v3"
	now = now.Add(time.Minute)
	v, err = get()
	require.NoError(t, err)
	require.Equal(t, "v3", v)
	require.Equal(t, []string{
		"INFO Fetched updated AWS Secrets Manager secret SecretID=id SecretType=api_key " +
			"PreviousVersionID=ver2 VersionID=ver2",
	}, logger.take())

	// Later successes aren't logged as recoveries
	now = now.Add(time.Minute)
	_, err = get()
	require.NoError(t, err)
	require.Empty(t, logger.take())
}

func TestSecretCacheInitialFetchError(t *testing.T) {
	fetchErr := error(&smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"})
	c := newSecretCache(funcFetcher(func(context.Context, string) (secretValue, error) {
		if fetchErr != nil {
			return secretValue{}, fetchErr
		}
		return secretValue{value: []byte("v"), versionID: "ver"}, nil
	}), secretIDs{ServerCACert: "id"}, time.Minute, time.Hour)
	logger := &recordingLogger{}
	metrics := newRecordingMetrics()
	tel := telemetry{logger: logger, metrics: metrics}
	_, err := c.getServerCACert(context.Background(), tel)
	require.ErrorContains(t, err, `failed fetching AWS Secrets Manager secret "id"`)
	require.ErrorContains(t, err, "denied")
	require.Empty(t, logger.take())
	require.Equal(t, int64(1), metrics.counter(metricFetchFailed, labelSecretType, secretTypeServerCACert,
		labelFetchPath, fetchPathSDK, labelFailureReason, failureAccessDenied, labelFallback, fallbackNone))
	require.Len(t, metrics.timer(metricFetchLatency, labelSecretType, secretTypeServerCACert,
		labelFetchPath, fetchPathSDK, labelOutcome, outcomeFailure), 1)
	require.Empty(t, metrics.counters(metricCacheLookup))

	// A first success after failures is logged only as a first fetch
	fetchErr = nil
	_, err = c.getServerCACert(context.Background(), tel)
	require.NoError(t, err)
	require.Equal(t, []string{
		"INFO Fetched AWS Secrets Manager secret SecretID=id SecretType=tls_server_ca_cert FetchPath=sdk VersionID=ver",
	}, logger.take())
}

func TestClientCertPair(t *testing.T) {
	certA, keyA := generateCert(t, "a")
	certB, keyB := generateCert(t, "b")
	fake := newFakeClient(map[string]string{"cert": certA, "key": keyA})
	c := newSecretCache(&sdkFetcher{client: fake}, secretIDs{ClientCert: "cert", ClientKey: "key"}, time.Minute,
		time.Hour)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	logger := &recordingLogger{}
	metrics := newRecordingMetrics()
	tel := telemetry{logger: logger, metrics: metrics}
	requireCert := func(expectedPEM string) {
		t.Helper()
		cert, err := c.getClientCert(context.Background(), tel)
		require.NoError(t, err)
		block, _ := pem.Decode([]byte(expectedPEM))
		require.Equal(t, block.Bytes, cert.Certificate[0])
	}
	setSecret := func(id, value string) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.secrets[id] = value
	}

	// Each secret is logged separately
	requireCert(certA)
	require.Equal(t, []string{
		"INFO Fetched AWS Secrets Manager secret SecretID=cert SecretType=tls_client_cert FetchPath=sdk VersionID=" +
			fakeVersionID(certA),
		"INFO Fetched AWS Secrets Manager secret SecretID=key SecretType=tls_client_key FetchPath=sdk VersionID=" +
			fakeVersionID(keyA),
	}, logger.take())

	// Fetching partway through rotation gets a mismatched pair, which falls back to the previous pair
	setSecret("cert", certB)
	now = now.Add(time.Minute)
	requireCert(certA)
	logs := logger.take()
	require.Len(t, logs, 2)
	require.Contains(t, logs[0], "WARN Failed refreshing AWS Secrets Manager secret, using previously fetched value "+
		"SecretID=cert SecretType=tls_client_cert")
	require.Contains(t, logs[1], "SecretID=key SecretType=tls_client_key")
	require.Equal(t, int64(1), metrics.counter(metricFetchFailed, labelSecretType, secretTypeClientCert,
		labelFetchPath, fetchPathSDK, labelFailureReason, failureInvalidResponse, labelFallback, fallbackStale))

	// Once rotation completes, the new pair is used after the retry interval. Both secrets are always fetched
	// together.
	setSecret("key", keyB)
	now = now.Add(failedRefreshRetryInterval - time.Second)
	requireCert(certA)
	require.Equal(t, 2, fake.callCount("cert"))
	now = now.Add(time.Second)
	requireCert(certB)
	require.Equal(t, []string{
		"INFO Fetched updated AWS Secrets Manager secret SecretID=cert SecretType=tls_client_cert " +
			"PreviousVersionID=" + fakeVersionID(certA) + " VersionID=" + fakeVersionID(certB),
		"INFO Fetched updated AWS Secrets Manager secret SecretID=key SecretType=tls_client_key " +
			"PreviousVersionID=" + fakeVersionID(keyA) + " VersionID=" + fakeVersionID(keyB),
	}, logger.take())
	require.Equal(t, 3, fake.callCount("cert"))
	require.Equal(t, 3, fake.callCount("key"))
}

func TestSecretCacheCallerCanceled(t *testing.T) {
	var fetches atomic.Int32
	var blocking atomic.Bool       // blocks the fetch until release is closed
	started := make(chan struct{}) // emits an event when a fetch blocks
	release := make(chan struct{}) // unblocks a blocked fetch
	c := newSecretCache(funcFetcher(func(ctx context.Context, _ string) (secretValue, error) {
		n := fetches.Add(1)
		if blocking.Load() {
			started <- struct{}{}
			<-release
		}
		if err := ctx.Err(); err != nil {
			return secretValue{}, err
		}
		return secretValue{value: fmt.Appendf(nil, "v%d", n), versionID: fmt.Sprint(n)}, nil
	}), secretIDs{APIKey: "id"}, time.Minute, time.Hour)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	metrics := newRecordingMetrics()
	tel := telemetry{metrics: metrics}
	lookups := func(result lookupResult) int64 {
		return metrics.counter(metricCacheLookup,
			labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathSDK, labelResult, result)
	}
	_, err := c.getAPIKey(context.Background(), tel)
	require.NoError(t, err)

	// A caller that gives up waiting for a refresh falls back to the stale value
	now = now.Add(time.Minute)
	blocking.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan string, 1)
	go func() {
		v, err := c.getAPIKey(ctx, tel)
		assert.NoError(t, err)
		result <- v
	}()
	<-started
	cancel()
	require.Equal(t, "v1", <-result)
	require.Equal(t, int64(1), lookups(lookupMissWithFallback))

	// The fetch isn't canceled along with the caller, and its result is cached
	c.apiKey.mu.Lock()
	r := c.apiKey.refreshState
	c.apiKey.mu.Unlock()
	close(release)
	<-r.done
	v, err := c.getAPIKey(context.Background(), tel)
	require.NoError(t, err)
	require.Equal(t, "v2", v)
	require.Equal(t, int32(2), fetches.Load())
	require.Equal(t, int64(1), lookups(lookupHit))
	require.Empty(t, metrics.counters(metricFetchFailed))
}

func TestSecretCacheFetchTimeout(t *testing.T) {
	var hang atomic.Bool
	c := newSecretCache(funcFetcher(func(ctx context.Context, _ string) (secretValue, error) {
		if hang.Load() {
			<-ctx.Done()
			return secretValue{}, ctx.Err()
		}
		return secretValue{value: []byte("v"), versionID: "ver"}, nil
	}), secretIDs{APIKey: "id"}, time.Minute, time.Hour)
	c.fetchTimeout = time.Millisecond
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	metrics := newRecordingMetrics()
	tel := telemetry{metrics: metrics}
	failures := func(fallback fallback) int64 {
		return metrics.counter(metricFetchFailed, labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathSDK,
			labelFailureReason, failureTimeout, labelFallback, fallback)
	}
	_, err := c.getAPIKey(context.Background(), tel)
	require.NoError(t, err)

	// A hung refresh times out even though the caller didn't, and is recorded as a failure with stale fallback
	now = now.Add(time.Minute)
	hang.Store(true)
	v, err := c.getAPIKey(context.Background(), tel)
	require.NoError(t, err)
	require.Equal(t, "v", v)
	require.Equal(t, int64(1), failures(fallbackStale))

	// Without a usable value, the timeout is returned
	now = now.Add(time.Hour)
	_, err = c.getAPIKey(context.Background(), tel)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int64(1), failures(fallbackNone))
}

func TestSecretCacheConcurrentRefresh(t *testing.T) {
	var fetches atomic.Int32
	var blocking atomic.Bool       // blocks the fetch until release is closed
	started := make(chan struct{}) // emits an event when a fetch first blocks
	release := make(chan struct{}) // unblocks a blocked fetch
	c := newSecretCache(funcFetcher(func(context.Context, string) (secretValue, error) {
		n := fetches.Add(1)
		if blocking.Load() {
			started <- struct{}{}
			<-release
		}
		// value/version increments with each fetch
		return secretValue{value: fmt.Appendf(nil, "v%d", n), versionID: fmt.Sprint(n)}, nil
	}), secretIDs{APIKey: "api-key", ServerCACert: "ca"}, time.Minute, time.Hour)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	metrics := newRecordingMetrics()
	// api-key and ca function as two independent entries, shouldn't block each other
	get := func(ctx context.Context, id string) (string, error) {
		tel := telemetry{metrics: metrics}
		if id == "ca" {
			b, err := c.getServerCACert(ctx, tel)
			return string(b), err
		}
		return c.getAPIKey(ctx, tel)
	}
	getAsync := func(id string) <-chan string {
		ch := make(chan string, 1)
		go func() {
			v, err := get(context.Background(), id)
			assert.NoError(t, err)
			ch <- v
		}()
		<-started
		return ch
	}

	// Nothing blocks, get returns normally
	v, err := get(context.Background(), "api-key")
	require.NoError(t, err)
	require.Equal(t, "v1", v)

	// Block an api-key refresh while in progress
	now = now.Add(time.Minute)
	blocking.Store(true)
	refreshed := getAsync("api-key")

	// While a refresh is in progress, other callers get the current value without waiting
	v, err = get(context.Background(), "api-key")
	require.NoError(t, err)
	require.Equal(t, "v1", v) // old value
	require.Equal(t, int64(1), metrics.counter(metricCacheLookup,
		labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathSDK, labelResult, lookupHit))

	// Block a ca refresh while in progress
	fetched := getAsync("ca")
	// Without a usable value, callers wait for the fetch in progress, and can give up
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = get(ctx, "ca")
	require.ErrorIs(t, err, context.Canceled)

	// Unblock the refreshes and verify the final results
	close(release)
	require.Equal(t, "v2", <-refreshed)
	require.Equal(t, "v3", <-fetched)
	require.Equal(t, int32(3), fetches.Load())
}
