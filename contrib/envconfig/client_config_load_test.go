package envconfig_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/contrib/envconfig"
)

func TestLoadClientOptionsFile(t *testing.T) {
	// Put some data in temp file and set the env var to use that file
	f, err := os.CreateTemp("", "")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	_, err = f.Write([]byte(`
[profile.default]
address = "my-address"
namespace = "my-namespace"`))
	f.Close()
	require.NoError(t, err)

	// Explicitly set
	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFilePath: f.Name(),
		EnvLookup:      EnvLookupMap{},
	})
	require.NoError(t, err)
	require.Equal(t, "my-address", opts.HostPort)
	require.Equal(t, "my-namespace", opts.Namespace)

	// From env
	opts, err = envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		EnvLookup: EnvLookupMap{"TEMPORAL_CONFIG_FILE": f.Name()},
	})
	require.NoError(t, err)
	require.Equal(t, "my-address", opts.HostPort)
	require.Equal(t, "my-namespace", opts.Namespace)
}

func TestLoadClientOptionsAPIKeyTLS(t *testing.T) {
	// Since API key is present, TLS defaults to present
	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData: []byte(`
		[profile.default]
		api_key = "my-api-key"`),
		EnvLookup: EnvLookupMap{},
	})
	require.NoError(t, err)
	require.NotNil(t, opts.Credentials)
	require.NotNil(t, opts.ConnectionOptions.TLS)

	// But when API key is not present, neither should TLS be
	opts, err = envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData: []byte(`
		[profile.default]
		address = "whatever"`),
		EnvLookup: EnvLookupMap{},
	})
	require.NoError(t, err)
	require.Nil(t, opts.Credentials)
	require.Nil(t, opts.ConnectionOptions.TLS)
}

func TestClientProfileApplyEnvVars(t *testing.T) {
	data := `
[profile.foo]
address = "my-address"
namespace = "my-namespace"
api_key = "my-api-key"
codec = { endpoint = "my-endpoint", auth = "my-auth" }
grpc_meta = { some-heAder1 = "some-value1", some-header2 = "some-value2", some_heaDer3 = "some-value3" }
some_future_key = "some future value not handled"

[profile.foo.tls]
disabled = true
client_cert_path = "my-client-cert-path"
client_cert_data = "my-client-cert-data"
client_key_path = "my-client-key-path"
client_key_data = "my-client-key-data"
server_ca_cert_path = "my-server-ca-cert-path"
server_ca_cert_data = "my-server-ca-cert-data"
server_name = "my-server-name"
disable_host_verification = true`
	// No env
	prof, err := envconfig.LoadClientConfigProfile(envconfig.LoadClientConfigProfileOptions{
		ConfigFileData:    []byte(data),
		ConfigFileProfile: "foo",
		EnvLookup:         EnvLookupMap{},
	})
	require.NoError(t, err)
	require.Equal(t, "my-address", prof.Address)
	require.Equal(t, "my-namespace", prof.Namespace)
	require.Equal(t, "my-api-key", prof.APIKey)
	require.Equal(t, "my-endpoint", prof.Codec.Endpoint)
	require.Equal(t, "my-auth", prof.Codec.Auth)
	require.True(t, prof.TLS.Disabled)
	require.Equal(t, "my-client-cert-path", prof.TLS.ClientCertPath)
	require.Equal(t, []byte("my-client-cert-data"), prof.TLS.ClientCertData)
	require.Equal(t, "my-client-key-path", prof.TLS.ClientKeyPath)
	require.Equal(t, []byte("my-client-key-data"), prof.TLS.ClientKeyData)
	require.Equal(t, "my-server-ca-cert-path", prof.TLS.ServerCACertPath)
	require.Equal(t, []byte("my-server-ca-cert-data"), prof.TLS.ServerCACertData)
	require.Equal(t, "my-server-name", prof.TLS.ServerName)
	require.True(t, prof.TLS.DisableHostVerification)
	require.Equal(t, map[string]string{
		"some-header1": "some-value1",
		"some-header2": "some-value2",
		"some-header3": "some-value3",
	}, prof.GRPCMeta)

	// With env
	prof, err = envconfig.LoadClientConfigProfile(envconfig.LoadClientConfigProfileOptions{
		ConfigFileData: []byte(data),
		EnvLookup: EnvLookupMap{
			"TEMPORAL_PROFILE":                       "foo",
			"TEMPORAL_ADDRESS":                       "my-address-new",
			"TEMPORAL_NAMESPACE":                     "my-namespace-new",
			"TEMPORAL_API_KEY":                       "my-api-key-new",
			"TEMPORAL_TLS":                           "true",
			"TEMPORAL_TLS_CLIENT_CERT_PATH":          "my-client-cert-path-new",
			"TEMPORAL_TLS_CLIENT_CERT_DATA":          "my-client-cert-data-new",
			"TEMPORAL_TLS_CLIENT_KEY_PATH":           "my-client-key-path-new",
			"TEMPORAL_TLS_CLIENT_KEY_DATA":           "my-client-key-data-new",
			"TEMPORAL_TLS_SERVER_CA_CERT_PATH":       "my-server-ca-cert-path-new",
			"TEMPORAL_TLS_SERVER_CA_CERT_DATA":       "my-server-ca-cert-data-new",
			"TEMPORAL_TLS_SERVER_NAME":               "my-server-name-new",
			"TEMPORAL_TLS_DISABLE_HOST_VERIFICATION": "false",
			"TEMPORAL_CODEC_ENDPOINT":                "my-endpoint-new",
			"TEMPORAL_CODEC_AUTH":                    "my-auth-new",
			// Leave first header alone, replace second header, set third as empty, add a fourth
			"TEMPORAL_GRPC_META_SOME_HEADER2": "some-value2-new",
			"TEMPORAL_GRPC_META_SOME_HEADER3": "",
			"TEMPORAL_GRPC_META_SOME_HEADER4": "some-value4-new",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "my-address-new", prof.Address)
	require.Equal(t, "my-namespace-new", prof.Namespace)
	require.Equal(t, "my-api-key-new", prof.APIKey)
	require.Equal(t, "my-endpoint-new", prof.Codec.Endpoint)
	require.Equal(t, "my-auth-new", prof.Codec.Auth)
	require.False(t, prof.TLS.Disabled)
	require.Equal(t, "my-client-cert-path-new", prof.TLS.ClientCertPath)
	require.Equal(t, []byte("my-client-cert-data-new"), prof.TLS.ClientCertData)
	require.Equal(t, "my-client-key-path-new", prof.TLS.ClientKeyPath)
	require.Equal(t, []byte("my-client-key-data-new"), prof.TLS.ClientKeyData)
	require.Equal(t, "my-server-ca-cert-path-new", prof.TLS.ServerCACertPath)
	require.Equal(t, []byte("my-server-ca-cert-data-new"), prof.TLS.ServerCACertData)
	require.Equal(t, "my-server-name-new", prof.TLS.ServerName)
	require.False(t, prof.TLS.DisableHostVerification)
	require.Equal(t, map[string]string{
		"some-header1": "some-value1",
		"some-header2": "some-value2-new",
		"some-header4": "some-value4-new",
	}, prof.GRPCMeta)
}

type EnvLookupMap map[string]string

func (e EnvLookupMap) Environ() []string {
	ret := make([]string, 0, len(e))
	for k, v := range e {
		ret = append(ret, k+"="+v)
	}
	return ret
}

func (e EnvLookupMap) LookupEnv(key string) (string, bool) {
	v, ok := e[key]
	return v, ok
}
