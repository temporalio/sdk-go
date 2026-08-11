package test_test

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

func TestEnvironmentFromEnvConfig(t *testing.T) {
	t.Setenv("TEMPORAL_TEST_ENV_CONFIG_SERVER", "true")
	t.Setenv("TEMPORAL_ADDRESS", "envconfig.example:7233")
	t.Setenv("TEMPORAL_NAMESPACE", "envconfig-namespace")
	t.Setenv("TEMPORAL_API_KEY", "envconfig-api-key")
	t.Setenv("TEMPORAL_TLS", "false")
	t.Setenv("TEMPORAL_GRPC_META_TEST_HEADER", "envconfig-test")
	t.Setenv("TEMPORAL_CLIENT_AUTHORITY", "envconfig-authority")

	environment := newTestEnvironment()
	config := environment.config
	options := environment.clientOptions

	require.Equal(t, "envconfig.example:7233", config.ServiceAddr)
	require.Equal(t, "envconfig-namespace", config.Namespace)
	require.False(t, config.ShouldRegisterNamespace)
	require.Equal(t, "envconfig.example:7233", options.HostPort)
	require.Equal(t, "envconfig-namespace", options.Namespace)
	require.NotNil(t, options.Credentials)
	require.True(t, options.ConnectionOptions.TLSDisabled)
	require.Nil(t, options.ConnectionOptions.TLS)
	require.Equal(t, "envconfig-authority", options.ConnectionOptions.Authority)

	require.NotNil(t, options.HeadersProvider)
	headers, err := options.HeadersProvider.GetHeaders(context.Background())
	require.NoError(t, err)
	require.Equal(t, "envconfig-test", headers["test-header"])
}

func TestEnvironmentFromEnvConfigCallbacksOverlay(t *testing.T) {
	t.Setenv("TEMPORAL_TEST_ENV_CONFIG_SERVER", "true")
	t.Setenv("TEMPORAL_ADDRESS", "envconfig.example:7233")
	t.Setenv("TEMPORAL_NAMESPACE", "envconfig-namespace")
	t.Setenv("TEMPORAL_API_KEY", "envconfig-api-key")
	t.Setenv("TEMPORAL_TLS", "true")
	t.Setenv("TEMPORAL_GRPC_META_TEST_HEADER", "envconfig-test")
	t.Setenv("TEMPORAL_CLIENT_AUTHORITY", "envconfig-authority")

	environment := newTestEnvironment(
		WithServiceAddr("127.0.0.1:7234"),
		WithNamespace("dedicated-test-namespace"),
	)
	config := environment.config
	options := environment.clientOptions

	require.Equal(t, "127.0.0.1:7234", config.ServiceAddr)
	require.Equal(t, "dedicated-test-namespace", config.Namespace)
	require.False(t, config.ShouldRegisterNamespace)
	require.NotNil(t, config.TLS)
	require.Equal(t, config.ServiceAddr, options.HostPort)
	require.Equal(t, config.Namespace, options.Namespace)
	require.NotNil(t, options.Credentials)
	require.NotNil(t, options.HeadersProvider)
	require.NotNil(t, options.ConnectionOptions.TLS)
	require.False(t, options.ConnectionOptions.TLSDisabled)
	require.Equal(t, "envconfig-authority", options.ConnectionOptions.Authority)
}

func TestEnvironmentFromConfigHarnessEnvironmentOverridesCallbacks(t *testing.T) {
	t.Setenv("TEMPORAL_TEST_ENV_CONFIG_SERVER", "false")
	t.Setenv("SERVICE_ADDR", "environment.example:7233")
	t.Setenv("TEMPORAL_NAMESPACE", "environment-namespace")
	t.Setenv("TEMPORAL_CLIENT_CERT", "")
	t.Setenv("TEMPORAL_CLIENT_KEY", "")
	t.Setenv("TEMPORAL_ADDRESS", "ignored-envconfig.example:7233")
	t.Setenv("TEMPORAL_API_KEY", "ignored-envconfig-api-key")

	environment := newTestEnvironment(
		WithServiceAddr("callback.example:7233"),
		WithNamespace("callback-namespace"),
	)
	config := environment.config
	options := environment.clientOptions

	require.Equal(t, "environment.example:7233", config.ServiceAddr)
	require.Equal(t, "environment-namespace", config.Namespace)
	require.False(t, config.ShouldRegisterNamespace)
	require.Equal(t, config.ServiceAddr, options.HostPort)
	require.Equal(t, config.Namespace, options.Namespace)
	require.Nil(t, options.Credentials)
	require.Nil(t, options.HeadersProvider)
}

func TestEnvironmentClientOptions(t *testing.T) {
	configTLS := &tls.Config{ServerName: "config.example"}
	environment := newTestEnvironmentFromConfig(Config{
		ServiceAddr: "config.example:7233",
		Namespace:   "config-namespace",
		TLS:         configTLS,
	})

	options := environment.newClientOptions(func(options *client.Options) {
		options.Namespace = "client-override-namespace"
	})

	require.Equal(t, environment.config.ServiceAddr, options.HostPort)
	require.Equal(t, "client-override-namespace", options.Namespace)
	require.Same(t, configTLS, options.ConnectionOptions.TLS)
	require.NotNil(t, options.Logger)
	require.Equal(t, "config-namespace", environment.clientOptions.Namespace)
}
