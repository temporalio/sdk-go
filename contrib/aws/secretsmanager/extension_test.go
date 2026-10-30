package secretsmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
)

type fakeClient struct {
	mu      sync.Mutex
	secrets map[string]string
	err     error
	calls   map[string]int
}

func newFakeClient(secrets map[string]string) *fakeClient {
	return &fakeClient{secrets: secrets, calls: map[string]int{}}
}

func (f *fakeClient) GetSecretValue(
	_ context.Context,
	params *awssm.GetSecretValueInput,
	_ ...func(*awssm.Options),
) (*awssm.GetSecretValueOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[*params.SecretId]++
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.secrets[*params.SecretId]
	if !ok {
		return nil, errors.New("secret not found")
	}
	return &awssm.GetSecretValueOutput{SecretString: &s, VersionId: aws.String(fakeVersionID(s))}, nil
}

// fakeVersionID derives a version ID from a secret value, so that changing a fake secret rotates it
func fakeVersionID(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:8]
}

func (f *fakeClient) callCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

type mapEnv map[string]string

func (m mapEnv) Environ() []string {
	ret := make([]string, 0, len(m))
	for k, v := range m {
		ret = append(ret, k+"="+v)
	}
	return ret
}

func (m mapEnv) LookupEnv(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func loadOptions(t *testing.T, ext Extension, toml string, env mapEnv) (client.Options, error) {
	t.Helper()
	if env == nil {
		env = mapEnv{}
	}
	return envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData:   []byte(toml),
		ConfigFileStrict: true,
		EnvLookup:        env,
		Extensions:       []envconfig.ClientConfigExtension{ext},
	})
}

func configure(t *testing.T, opts *client.Options) error {
	t.Helper()
	for _, p := range opts.Plugins {
		if err := p.ConfigureClient(context.Background(), client.PluginConfigureClientOptions{ClientOptions: opts}); err != nil {
			return err
		}
	}
	return nil
}

func TestLoadSecretIDs(t *testing.T) {
	toml := `
[profile.default.ext.aws_secrets_manager]
api_key_id = "toml-api-key"
[profile.default.ext.aws_secrets_manager.tls]
client_cert_id = "toml-cert"
client_key_id = "toml-key"
server_ca_cert_id = "toml-ca"
`
	load := func(toml string, env mapEnv) (secretIDs, error) {
		var conf envconfig.ClientConfig
		require.NoError(t, conf.FromTOML([]byte(toml), envconfig.ClientConfigFromTOMLOptions{}))
		prof := conf.Profiles["default"]
		if prof == nil {
			prof = &envconfig.ClientConfigProfile{}
		}
		if env != nil {
			require.NoError(t, prof.ApplyEnvVars(env))
		}
		req := envconfig.ClientConfigExtensionRequest{Profile: prof}
		if env != nil {
			req.Env = env
		}
		return loadSecretIDs(req)
	}

	ids, err := load(toml, nil)
	require.NoError(t, err)
	require.Equal(t, secretIDs{
		APIKey: "toml-api-key", ClientCert: "toml-cert", ClientKey: "toml-key", ServerCACert: "toml-ca",
	}, ids)

	// Env overrides file, and set-but-empty env unsets
	ids, err = load(toml, mapEnv{
		envAPIKeyID:       "env-api-key",
		envClientCertID:   "env-bundle",
		envClientKeyID:    "env-bundle",
		envServerCACertID: "",
	})
	require.NoError(t, err)
	require.Equal(t, secretIDs{APIKey: "env-api-key", ClientCert: "env-bundle", ClientKey: "env-bundle"}, ids)

	ids, err = load("", nil)
	require.NoError(t, err)
	require.Equal(t, secretIDs{}, ids)

	for name, tc := range map[string]struct {
		toml string
		env  mapEnv
		err  string
	}{
		"unknown key": {
			toml: "[profile.default.ext.aws_secrets_manager]\napi_key = \"x\"",
			err:  `unknown key "api_key" in ext.aws_secrets_manager`,
		},
		"unknown tls key": {
			toml: "[profile.default.ext.aws_secrets_manager.tls]\nclient_cert = \"x\"",
			err:  `unknown key "client_cert" in ext.aws_secrets_manager.tls`,
		},
		"non-string": {
			toml: "[profile.default.ext.aws_secrets_manager]\napi_key_id = 5",
			err:  "ext.aws_secrets_manager.api_key_id must be a string",
		},
		"non-table tls": {
			toml: "[profile.default.ext.aws_secrets_manager]\ntls = \"x\"",
			err:  "ext.aws_secrets_manager.tls must be a table",
		},
		"api key conflict in file": {
			toml: "[profile.default]\napi_key = \"k\"\n[profile.default.ext.aws_secrets_manager]\napi_key_id = \"x\"",
			err:  "API key secret ID cannot be set with an API key",
		},
		"api key conflict across file and env": {
			toml: "[profile.default]\napi_key = \"k\"",
			env:  mapEnv{envAPIKeyID: "x"},
			err:  "API key secret ID cannot be set with an API key",
		},
		"api key conflict in env": {
			env: mapEnv{"TEMPORAL_API_KEY": "k", envAPIKeyID: "x"},
			err: "API key secret ID cannot be set with an API key",
		},
		"cert conflict": {
			env: mapEnv{"TEMPORAL_TLS_CLIENT_CERT_DATA": "c", envClientCertID: "x", envClientKeyID: "x"},
			err: "TLS client cert secret ID cannot be set with client cert path or data",
		},
		"key conflict": {
			env: mapEnv{"TEMPORAL_TLS_CLIENT_KEY_PATH": "k", envClientCertID: "x", envClientKeyID: "x"},
			err: "TLS client key secret ID cannot be set with client key path or data",
		},
		"ca conflict": {
			env: mapEnv{"TEMPORAL_TLS_SERVER_CA_CERT_PATH": "ca", envServerCACertID: "x"},
			err: "TLS server CA cert secret ID cannot be set with server CA cert path or data",
		},
		"cert without key": {
			env: mapEnv{envClientCertID: "x"},
			err: "TLS client cert and key secret IDs must be set together",
		},
		"tls disabled": {
			env: mapEnv{"TEMPORAL_TLS": "false", envServerCACertID: "x"},
			err: "TLS secret IDs cannot be set when TLS is disabled",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(tc.toml, tc.env)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestNoConfig(t *testing.T) {
	opts, err := loadOptions(t, Extension{SecretsClient: newFakeClient(nil)}, "", nil)
	require.NoError(t, err)
	require.Empty(t, opts.Plugins)
}

func TestAPIKey(t *testing.T) {
	// Capture headers on a fake server to observe the dynamic credentials
	lis, err := net.Listen("tcp", "127.0.0.1:0") // random available ephemeral port on localhost
	require.NoError(t, err)
	authHeaders := make(chan string, 10)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		authHeaders <- strings.Join(md.Get("authorization"), ",")
		return status.Error(codes.Unimplemented, "unimplemented")
	}))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	fake := newFakeClient(map[string]string{"api-key-secret": "my-api-key"})
	opts, err := loadOptions(t, Extension{SecretsClient: fake}, `
[profile.default]
address = "`+lis.Addr().String()+`"
[profile.default.tls]
disabled = true
[profile.default.ext.aws_secrets_manager]
api_key_id = "api-key-secret"
`, nil)
	require.NoError(t, err)
	require.Len(t, opts.Plugins, 1)

	c, _ := client.Dial(opts)
	if c != nil {
		c.Close()
	}
	select {
	case h := <-authHeaders:
		require.Equal(t, "Bearer my-api-key", h)
	case <-time.After(10 * time.Second):
		t.Fatal("no request received")
	}
	require.Equal(t, 1, fake.callCount("api-key-secret"))
}

func TestAPIKeyEnablesTLS(t *testing.T) {
	fake := newFakeClient(map[string]string{"api-key-secret": "my-api-key"})
	opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{envAPIKeyID: "api-key-secret"})
	require.NoError(t, err)
	require.Nil(t, opts.ConnectionOptions.TLS)
	require.NoError(t, configure(t, &opts))
	require.NotNil(t, opts.Credentials)
	require.NotNil(t, opts.ConnectionOptions.TLS)
}

func TestAPIKeyFetchFailure(t *testing.T) {
	opts, err := loadOptions(t, Extension{SecretsClient: newFakeClient(nil)}, "", mapEnv{envAPIKeyID: "missing"})
	require.NoError(t, err)
	require.ErrorContains(t, configure(t, &opts), `failed fetching AWS Secrets Manager secret "missing"`)
}

func TestAPIKeyCredentialsAlreadySet(t *testing.T) {
	fake := newFakeClient(map[string]string{"api-key-secret": "my-api-key"})
	opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{envAPIKeyID: "api-key-secret"})
	require.NoError(t, err)
	opts.Credentials = client.NewAPIKeyStaticCredentials("other")
	require.ErrorContains(t, configure(t, &opts), "client credentials already set")
}

func TestTLS(t *testing.T) {
	certPEM, keyPEM := generateCert(t, "client")
	caPEM, _ := generateCert(t, "ca")

	t.Run("separate cert and key", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"cert": certPEM, "key": keyPEM, "ca": caPEM})
		opts, err := loadOptions(t, Extension{SecretsClient: fake}, `
[profile.default.tls]
server_name = "my-server"
[profile.default.ext.aws_secrets_manager.tls]
client_cert_id = "cert"
client_key_id = "key"
server_ca_cert_id = "ca"
`, nil)
		require.NoError(t, err)
		require.NoError(t, configure(t, &opts))
		conf := opts.ConnectionOptions.TLS
		require.NotNil(t, conf)
		require.Equal(t, "my-server", conf.ServerName)
		require.NotNil(t, conf.RootCAs)
		requireClientCert(t, conf, certPEM)
		require.Equal(t, 1, fake.callCount("cert"))
		require.Equal(t, 1, fake.callCount("key"))
		require.Equal(t, 1, fake.callCount("ca"))
	})

	t.Run("bundled cert and key", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"bundle": certPEM + keyPEM})
		opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{
			envClientCertID: "bundle",
			envClientKeyID:  "bundle",
		})
		require.NoError(t, err)
		require.NoError(t, configure(t, &opts))
		conf := opts.ConnectionOptions.TLS
		require.NotNil(t, conf)
		require.Nil(t, conf.RootCAs)
		requireClientCert(t, conf, certPEM)
		require.Equal(t, 1, fake.callCount("bundle"))
	})

	t.Run("refreshes cert", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"bundle": certPEM + keyPEM})
		opts, err := loadOptions(t, Extension{SecretsClient: fake, RefreshInterval: time.Nanosecond}, "", mapEnv{
			envClientCertID: "bundle",
			envClientKeyID:  "bundle",
		})
		require.NoError(t, err)
		require.NoError(t, configure(t, &opts))
		newCertPEM, newKeyPEM := generateCert(t, "client2")
		fake.mu.Lock()
		fake.secrets["bundle"] = newCertPEM + newKeyPEM
		fake.mu.Unlock()
		requireClientCert(t, opts.ConnectionOptions.TLS, newCertPEM)
		// Every get refreshes at this interval, so this checks the bundle is fetched once per cert load, not once
		// each for cert and key
		require.Equal(t, 2, fake.callCount("bundle"))
	})

	t.Run("invalid key pair", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"cert": certPEM, "key": "not a key"})
		opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{
			envClientCertID: "cert",
			envClientKeyID:  "key",
		})
		require.NoError(t, err)
		require.ErrorContains(t, configure(t, &opts), `failed loading TLS client cert/key from secrets "cert" and "key"`)
	})

	t.Run("invalid CA", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"ca": "not a cert"})
		opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{envServerCACertID: "ca"})
		require.NoError(t, err)
		require.ErrorContains(t, configure(t, &opts), `no PEM certificates found in TLS server CA cert secret "ca"`)
	})

	t.Run("TLS disabled after load", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"ca": caPEM})
		opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{envServerCACertID: "ca"})
		require.NoError(t, err)
		opts.ConnectionOptions.TLSDisabled = true
		require.ErrorContains(t, configure(t, &opts), "TLS is disabled")
	})

	t.Run("does not mutate original TLS config", func(t *testing.T) {
		fake := newFakeClient(map[string]string{"ca": caPEM})
		opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{envServerCACertID: "ca"})
		require.NoError(t, err)
		orig := &tls.Config{ServerName: "orig"}
		opts.ConnectionOptions.TLS = orig
		require.NoError(t, configure(t, &opts))
		require.Nil(t, orig.RootCAs)
		require.NotNil(t, opts.ConnectionOptions.TLS.RootCAs)
		require.Equal(t, "orig", opts.ConnectionOptions.TLS.ServerName)
	})
}

func TestTelemetryFromClientOptions(t *testing.T) {
	certPEM, keyPEM := generateCert(t, "client")
	fake := newFakeClient(map[string]string{"bundle": certPEM + keyPEM})
	opts, err := loadOptions(t, Extension{SecretsClient: fake, RefreshInterval: time.Nanosecond}, "", mapEnv{
		envClientCertID: "bundle",
		envClientKeyID:  "bundle",
	})
	require.NoError(t, err)
	logger := &recordingLogger{}
	metrics := newRecordingMetrics()
	opts.Logger = logger
	opts.MetricsHandler = metrics
	require.NoError(t, configure(t, &opts))
	require.Equal(t, []string{
		"INFO Fetched AWS Secrets Manager secret SecretID=bundle SecretType=tls_client_cert FetchPath=sdk " +
			"VersionID=" + fakeVersionID(certPEM+keyPEM),
	}, logger.take())

	// Rotation during the client's lifetime is logged
	newCertPEM, newKeyPEM := generateCert(t, "client2")
	fake.mu.Lock()
	fake.secrets["bundle"] = newCertPEM + newKeyPEM
	fake.mu.Unlock()
	requireClientCert(t, opts.ConnectionOptions.TLS, newCertPEM)
	require.Equal(t, []string{
		"INFO Fetched updated AWS Secrets Manager secret SecretID=bundle SecretType=tls_client_cert " +
			"PreviousVersionID=" + fakeVersionID(certPEM+keyPEM) + " VersionID=" + fakeVersionID(newCertPEM+newKeyPEM),
	}, logger.take())

	// Bundled cert/key is fetched once and labeled as the client cert
	require.Equal(t, map[string]int64{
		"temporal_aws_secrets_manager_cache_lookup fetch_path=sdk result=miss secret_type=tls_client_cert": 2,
	}, metrics.counters(metricCacheLookup))
}

func requireClientCert(t *testing.T, conf *tls.Config, expectedPEM string) {
	t.Helper()
	require.NotNil(t, conf.GetClientCertificate)
	cert, err := conf.GetClientCertificate(&tls.CertificateRequestInfo{})
	require.NoError(t, err)
	block, _ := pem.Decode([]byte(expectedPEM))
	require.Equal(t, block.Bytes, cert.Certificate[0])
}

func generateCert(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func TestRefreshIntervalFromEnv(t *testing.T) {
	for name, tc := range map[string]struct {
		set      bool
		env      string
		field    time.Duration
		expected time.Duration
		err      bool
	}{
		"unset":          {expected: defaultRefreshInterval},
		"empty":          {set: true, env: "", expected: defaultRefreshInterval},
		"seconds":        {set: true, env: "60", expected: time.Minute},
		"zero":           {set: true, env: "0", expected: 0},
		"over 300":       {set: true, env: "3600", expected: time.Hour},
		"field wins":     {set: true, env: "60", field: time.Hour, expected: time.Hour},
		"field wins bad": {set: true, env: "bad", field: time.Hour, expected: time.Hour},
		"negative":       {set: true, env: "-1", err: true},
		"fractional":     {set: true, env: "1.5", err: true},
		"not a number":   {set: true, env: "5m", err: true},
		"overflow":       {set: true, env: "9223372036854775807", err: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envSecretsManagerTTL, tc.env)
			if !tc.set {
				require.NoError(t, os.Unsetenv(envSecretsManagerTTL))
			}
			d, err := Extension{RefreshInterval: tc.field}.refreshInterval()
			if tc.err {
				require.ErrorContains(t, err, "invalid SECRETS_MANAGER_TTL")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, d)
		})
	}
}

func TestRefreshIntervalZeroDisablesCaching(t *testing.T) {
	t.Setenv(envSecretsManagerTTL, "0")
	certPEM, keyPEM := generateCert(t, "client")
	fake := newFakeClient(map[string]string{"bundle": certPEM + keyPEM})
	opts, err := loadOptions(t, Extension{SecretsClient: fake}, "", mapEnv{
		envClientCertID: "bundle",
		envClientKeyID:  "bundle",
	})
	require.NoError(t, err)
	require.NoError(t, configure(t, &opts))
	requireClientCert(t, opts.ConnectionOptions.TLS, certPEM)
	requireClientCert(t, opts.ConnectionOptions.TLS, certPEM)
	require.Equal(t, 3, fake.callCount("bundle"))
}
