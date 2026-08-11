package test_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewConfigEnvConfigServer(t *testing.T) {
	t.Setenv("TEMPORAL_TEST_ENV_CONFIG_SERVER", "true")
	t.Setenv("TEMPORAL_ADDRESS", "envconfig.example:7233")
	t.Setenv("TEMPORAL_NAMESPACE", "envconfig-namespace")
	t.Setenv("TEMPORAL_API_KEY", "envconfig-api-key")
	t.Setenv("TEMPORAL_TLS", "false")
	t.Setenv("TEMPORAL_GRPC_META_TEST_HEADER", "envconfig-test")
	t.Setenv("TEMPORAL_CLIENT_AUTHORITY", "envconfig-authority")

	config := NewConfig()

	require.Equal(t, "envconfig.example:7233", config.ServiceAddr)
	require.Equal(t, "envconfig-namespace", config.Namespace)
	require.False(t, config.ShouldRegisterNamespace)
	require.Equal(t, "envconfig.example:7233", config.clientOptions.HostPort)
	require.Equal(t, "envconfig-namespace", config.clientOptions.Namespace)
	require.NotNil(t, config.clientOptions.Credentials)
	require.True(t, config.clientOptions.ConnectionOptions.TLSDisabled)
	require.Nil(t, config.clientOptions.ConnectionOptions.TLS)
	require.Equal(t, "envconfig-authority", config.clientOptions.ConnectionOptions.Authority)

	require.NotNil(t, config.clientOptions.HeadersProvider)
	headers, err := config.clientOptions.HeadersProvider.GetHeaders(context.Background())
	require.NoError(t, err)
	require.Equal(t, "envconfig-test", headers["test-header"])
}

func TestNewConfigEnvConfigServerCallbackServer(t *testing.T) {
	t.Setenv("TEMPORAL_TEST_ENV_CONFIG_SERVER", "true")
	t.Setenv("TEMPORAL_ADDRESS", "envconfig.example:7233")
	t.Setenv("TEMPORAL_NAMESPACE", "envconfig-namespace")
	t.Setenv("TEMPORAL_API_KEY", "envconfig-api-key")
	t.Setenv("TEMPORAL_TLS", "true")
	t.Setenv("TEMPORAL_GRPC_META_TEST_HEADER", "envconfig-test")
	t.Setenv("TEMPORAL_CLIENT_AUTHORITY", "envconfig-authority")

	config := NewConfig(
		WithServiceAddr("127.0.0.1:7234"),
		WithNamespace("dedicated-test-namespace"),
	)

	require.Equal(t, "127.0.0.1:7234", config.ServiceAddr)
	require.Equal(t, "dedicated-test-namespace", config.Namespace)
	require.True(t, config.ShouldRegisterNamespace)
	require.Nil(t, config.TLS)
	require.Equal(t, config.ServiceAddr, config.clientOptions.HostPort)
	require.Equal(t, config.Namespace, config.clientOptions.Namespace)
	require.Nil(t, config.clientOptions.Credentials)
	require.Nil(t, config.clientOptions.HeadersProvider)
	require.Nil(t, config.clientOptions.ConnectionOptions.TLS)
	require.False(t, config.clientOptions.ConnectionOptions.TLSDisabled)
	require.Empty(t, config.clientOptions.ConnectionOptions.Authority)
}
