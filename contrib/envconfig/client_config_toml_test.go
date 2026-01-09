package envconfig

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

	var conf ClientConfig
	require.NoError(t, conf.FromTOML([]byte(data), ClientConfigFromTOMLOptions{}))
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
	b, err := conf.ToTOML(ClientConfigToTOMLOptions{})
	require.NoError(t, err)
	var newConf ClientConfig
	require.NoError(t, newConf.FromTOML(b, ClientConfigFromTOMLOptions{}))
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

	var conf ClientConfig
	err := conf.FromTOML([]byte(data), ClientConfigFromTOMLOptions{Strict: true})
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

	var conf ClientConfig
	require.NoError(t, conf.FromTOML([]byte(data), ClientConfigFromTOMLOptions{}))
	prof := conf.Profiles["foo"]
	require.Empty(t, prof.Address)
	require.Empty(t, prof.Namespace)
	require.Equal(t, "my-api-key", prof.APIKey)
	require.Nil(t, prof.Codec)
	require.NotNil(t, prof.TLS)
	require.Zero(t, *prof.TLS)

	// Back to toml and back to structure again, then deep equality check
	b, err := conf.ToTOML(ClientConfigToTOMLOptions{})
	require.NoError(t, err)
	var newConf ClientConfig
	require.NoError(t, newConf.FromTOML(b, ClientConfigFromTOMLOptions{}))
	require.Equal(t, conf, newConf)
}

func TestClientConfigTOMLEmpty(t *testing.T) {
	var conf ClientConfig
	require.NoError(t, conf.FromTOML(nil, ClientConfigFromTOMLOptions{}))
	require.Empty(t, conf.Profiles)

	// Back to toml and back to structure again, then deep equality check
	b, err := conf.ToTOML(ClientConfigToTOMLOptions{})
	require.NoError(t, err)
	var newConf ClientConfig
	require.NoError(t, newConf.FromTOML(b, ClientConfigFromTOMLOptions{}))
	require.Equal(t, conf, newConf)
}

func TestClientConfigTOMLAdditionalProfileFields(t *testing.T) {
	data := `
[profile.foo]
address = "my-address"
namespace = "my-namespace"
custom_field = "custom-value"
custom_field2 = 42

[profile.foo.custom_nested]
key1 = "value1"

[profile.foo.custom_nested.deep]
key2 = "value2"

[profile.foo.custom_nested.deep.deeper]
key3 = "value3"

[profile.bar]
address = "bar-address"
custom_field = true`

	var conf ClientConfig
	additional := make(map[string]map[string]any)
	require.NoError(t, conf.FromTOML([]byte(data), ClientConfigFromTOMLOptions{
		AdditionalProfileFields: additional,
	}))

	// Verify known fields were parsed
	require.Equal(t, "my-address", conf.Profiles["foo"].Address)
	require.Equal(t, "my-namespace", conf.Profiles["foo"].Namespace)
	require.Equal(t, "bar-address", conf.Profiles["bar"].Address)

	// Verify additional fields were captured
	require.Equal(t, "custom-value", additional["foo"]["custom_field"])
	require.Equal(t, int64(42), additional["foo"]["custom_field2"])
	require.Equal(t, true, additional["bar"]["custom_field"])

	// Verify deeply nested additional fields are preserved
	customNested, ok := additional["foo"]["custom_nested"].(map[string]any)
	require.True(t, ok, "custom_nested should be a map")
	require.Equal(t, "value1", customNested["key1"])

	deep, ok := customNested["deep"].(map[string]any)
	require.True(t, ok, "custom_nested.deep should be a map")
	require.Equal(t, "value2", deep["key2"])

	deeper, ok := deep["deeper"].(map[string]any)
	require.True(t, ok, "custom_nested.deep.deeper should be a map")
	require.Equal(t, "value3", deeper["key3"])

	// Back to TOML and back to structure again, then deep equality check
	b, err := conf.ToTOML(ClientConfigToTOMLOptions{
		AdditionalProfileFields: additional,
	})
	require.NoError(t, err)
	var newConf ClientConfig
	newAdditional := make(map[string]map[string]any)
	require.NoError(t, newConf.FromTOML(b, ClientConfigFromTOMLOptions{
		AdditionalProfileFields: newAdditional,
	}))
	require.Equal(t, conf, newConf)
	require.Equal(t, additional, newAdditional)
}

func TestClientConfigTOMLAdditionalProfileFieldsConflict(t *testing.T) {
	conf := ClientConfig{
		Profiles: map[string]*ClientConfigProfile{
			"foo": {Address: "my-address"},
		},
	}

	// Attempt to write with an additional field that conflicts with a known field
	_, err := conf.ToTOML(ClientConfigToTOMLOptions{
		AdditionalProfileFields: map[string]map[string]any{
			"foo": {"address": "conflict"},
		},
	})
	require.ErrorContains(t, err, "additional field \"address\" in profile \"foo\" conflicts with known profile field")
}

func TestKnownProfileKeysInSync(t *testing.T) {
	// Extract keys from tomlClientConfigProfile's TOML tags using reflection
	expected := make(map[string]bool)
	typ := reflect.TypeFor[tomlClientConfigProfile]()
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("toml")
		if key, _, _ := strings.Cut(tag, ","); key != "" {
			expected[key] = true
		}
	}

	require.Equal(t, expected, knownProfileKeys,
		"knownProfileKeys must match tomlClientConfigProfile's TOML tags")
}
