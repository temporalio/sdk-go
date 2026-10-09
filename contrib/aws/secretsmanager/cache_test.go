package secretsmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/smithy-go"
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
	}), time.Minute, time.Hour)
	c.now = func() time.Time { return now }
	logger := &recordingLogger{}
	metrics := newRecordingMetrics()
	tel := telemetry{logger: logger, metrics: metrics}
	get := func() (string, error) {
		b, err := c.get(context.Background(), "id", secretTypeAPIKey, tel)
		return string(b), err
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
			"SecretType=api_key Error=outage Age=1m0.001s MaxStaleness=1h0m0s",
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
	_, err = c.get(context.Background(), "id", secretTypeAPIKey, telemetry{})
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
	}), time.Minute, time.Hour)
	logger := &recordingLogger{}
	metrics := newRecordingMetrics()
	tel := telemetry{logger: logger, metrics: metrics}
	_, err := c.get(context.Background(), "id", secretTypeServerCACert, tel)
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
	_, err = c.get(context.Background(), "id", secretTypeServerCACert, tel)
	require.NoError(t, err)
	require.Equal(t, []string{
		"INFO Fetched AWS Secrets Manager secret SecretID=id SecretType=tls_server_ca_cert FetchPath=sdk VersionID=ver",
	}, logger.take())
}
