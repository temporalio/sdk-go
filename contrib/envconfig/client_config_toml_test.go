package envconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/contrib/envconfig"
)

func TestClientConfigTOMLFull(t *testing.T) {
	data := `
[profile.foo]
address = "my-address"
namespace = "my-namespace"
api_key = "my-api-key"
codec = { endpoint = "my-endpoint", auth = "my-auth" }
grpc_meta = { sOme-hEader_key = "some-value" }
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

	var conf envconfig.ClientConfig
	require.NoError(t, conf.FromTOML([]byte(data), envconfig.ClientConfigFromTOMLOptions{}))
	prof := conf.Profiles["foo"]
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
	require.Equal(t, map[string]string{"some-header-key": "some-value"}, prof.GRPCMeta)

	// Back to toml and back to structure again, then deep equality check
	b, err := conf.ToTOML(envconfig.ClientConfigToTOMLOptions{})
	require.NoError(t, err)
	var newConf envconfig.ClientConfig
	require.NoError(t, newConf.FromTOML(b, envconfig.ClientConfigFromTOMLOptions{}))
	require.Equal(t, conf, newConf)
	// Sanity check that require.Equal actually does deep-equality
	newConf.Profiles["foo"].Codec.Auth += "-dirty"
	require.NotEqual(t, conf, newConf)
}

func TestClientConfigTOMLStrict(t *testing.T) {
	data := `
[unimportant]
stuff = "does not matter"

[profile.foo]
address = "my-address"
some_future_key = "some value"`

	var conf envconfig.ClientConfig
	err := conf.FromTOML([]byte(data), envconfig.ClientConfigFromTOMLOptions{Strict: true})
	require.ErrorContains(t, err, "unimportant.stuff")
	require.ErrorContains(t, err, "profile.foo.some_future_key")
}

func TestClientConfigTOMLPartial(t *testing.T) {
	// Partial data
	data := `
[profile.foo]
api_key = "my-api-key"

[profile.foo.tls]
`

	var conf envconfig.ClientConfig
	require.NoError(t, conf.FromTOML([]byte(data), envconfig.ClientConfigFromTOMLOptions{}))
	prof := conf.Profiles["foo"]
	require.Empty(t, prof.Address)
	require.Empty(t, prof.Namespace)
	require.Equal(t, "my-api-key", prof.APIKey)
	require.Nil(t, prof.Codec)
	require.NotNil(t, prof.TLS)
	require.Zero(t, *prof.TLS)

	// Back to toml and back to structure again, then deep equality check
	b, err := conf.ToTOML(envconfig.ClientConfigToTOMLOptions{})
	require.NoError(t, err)
	var newConf envconfig.ClientConfig
	require.NoError(t, newConf.FromTOML(b, envconfig.ClientConfigFromTOMLOptions{}))
	require.Equal(t, conf, newConf)
}

func TestClientConfigTOMLEmpty(t *testing.T) {
	var conf envconfig.ClientConfig
	require.NoError(t, conf.FromTOML(nil, envconfig.ClientConfigFromTOMLOptions{}))
	require.Empty(t, conf.Profiles)

	// Back to toml and back to structure again, then deep equality check
	b, err := conf.ToTOML(envconfig.ClientConfigToTOMLOptions{})
	require.NoError(t, err)
	var newConf envconfig.ClientConfig
	require.NoError(t, newConf.FromTOML(b, envconfig.ClientConfigFromTOMLOptions{}))
	require.Equal(t, conf, newConf)
}
