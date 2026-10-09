package secretsmanager

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/contrib/envconfig"
)

func TestLambdaExtensionFetcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/secretsmanager/get" || r.Header.Get(lambdaExtensionTokenHdr) != "session-token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Query().Get("secretId") {
		case "arn:aws:secretsmanager:us-east-1:123:secret:string":
			_ = json.NewEncoder(w).Encode(map[string]any{"SecretString": "string-value", "VersionId": "ver1"})
		case "binary":
			_ = json.NewEncoder(w).Encode(map[string]any{"SecretBinary": []byte("binary-value")})
		case "empty":
			_ = json.NewEncoder(w).Encode(map[string]any{"VersionId": "ver1"})
		case "bad-json":
			_, _ = w.Write([]byte("{"))
		case "server-error":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	t.Setenv(envLambdaExtensionPort, u.Port())
	t.Setenv(envAWSSessionToken, "session-token")

	f := newLambdaExtensionFetcher(u.Port())
	require.Equal(t, fetchPathExtension, f.path())
	v, err := f.fetch(context.Background(), "arn:aws:secretsmanager:us-east-1:123:secret:string")
	require.NoError(t, err)
	require.Equal(t, secretValue{value: []byte("string-value"), versionID: "ver1"}, v)
	v, err = f.fetch(context.Background(), "binary")
	require.NoError(t, err)
	require.Equal(t, secretValue{value: []byte("binary-value")}, v)
	_, err = f.fetch(context.Background(), "missing")
	require.ErrorContains(t, err, "returned status 400: bad request")
	require.Equal(t, failureClientError, classifyFailure(err))
	_, err = f.fetch(context.Background(), "server-error")
	require.ErrorContains(t, err, "returned status 500: internal error")
	require.Equal(t, failureServerError, classifyFailure(err))
	_, err = f.fetch(context.Background(), "empty")
	require.ErrorContains(t, err, "secret has no value")
	require.Equal(t, failureInvalidResponse, classifyFailure(err))
	_, err = f.fetch(context.Background(), "bad-json")
	require.ErrorContains(t, err, "failed decoding")
	require.Equal(t, failureInvalidResponse, classifyFailure(err))

	// Extension is used when no client is given and the port env var is set
	// (configure triggers an eager load, which exercises the fetcher)
	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		DisableFile: true,
		EnvLookup:   mapEnv{envAPIKeyID: "arn:aws:secretsmanager:us-east-1:123:secret:string"},
		Extensions:  []envconfig.ClientConfigExtension{Extension{}},
	})
	require.NoError(t, err)
	metrics := newRecordingMetrics()
	opts.MetricsHandler = metrics
	require.NoError(t, configure(t, &opts))
	require.NotNil(t, opts.Credentials)
	require.Equal(t, int64(1), metrics.counter(metricCacheLookup,
		labelSecretType, secretTypeAPIKey, labelFetchPath, fetchPathExtension, labelResult, lookupMiss))
}

func TestDefaultFetcherDetection(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0") // random available ephemeral port
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() })
	_, listeningPort, err := net.SplitHostPort(lis.Addr().String())
	require.NoError(t, err)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, closedPort, err := net.SplitHostPort(closed.Addr().String())
	require.NoError(t, err)
	require.NoError(t, closed.Close())

	for name, tc := range map[string]struct {
		set          bool
		env          string
		defaultPort  string
		expectedPort string
	}{
		"unset, default port listening": {defaultPort: listeningPort, expectedPort: listeningPort},
		"unset, default port closed":    {defaultPort: closedPort},
		"set, not probed":               {set: true, env: "1234", defaultPort: closedPort, expectedPort: "1234"},
		"empty, default port listening": {set: true, env: "", defaultPort: listeningPort},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envLambdaExtensionPort, tc.env)
			if !tc.set {
				require.NoError(t, os.Unsetenv(envLambdaExtensionPort))
			}

			// We don't want to listen on a fixed well-known port in a test, so modify
			// the default port to this test's ephemeral one
			origDefault := defaultLambdaExtensionPort
			defaultLambdaExtensionPort = tc.defaultPort
			t.Cleanup(func() { defaultLambdaExtensionPort = origDefault })

			f := newDefaultFetcher()
			if tc.expectedPort == "" {
				require.IsType(t, &sdkFetcher{}, f)
				return
			}
			require.IsType(t, &lambdaExtensionFetcher{}, f)
			require.Equal(t, "http://localhost:"+tc.expectedPort+"/secretsmanager/get",
				f.(*lambdaExtensionFetcher).endpoint)
		})
	}
}

func TestSDKFetcher(t *testing.T) {
	f := &sdkFetcher{client: newFakeClient(map[string]string{"id": "value"})}
	require.Equal(t, fetchPathSDK, f.path())
	v, err := f.fetch(context.Background(), "id")
	require.NoError(t, err)
	require.Equal(t, secretValue{value: []byte("value"), versionID: fakeVersionID("value")}, v)
}
