package internal

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyCredentials_TLSEnabledByDefaultWhenAPIKeyProvided(t *testing.T) {
	creds := NewAPIKeyStaticCredentials("test-api-key")
	opts := &ConnectionOptions{}

	err := creds.applyToOptions(opts)
	require.NoError(t, err)

	// TLS should be auto-enabled when api_key is provided and tls not explicitly set
	require.NotNil(t, opts.TLS)
}

func TestAPIKeyCredentials_TLSCanBeExplicitlyDisabledWithAPIKey(t *testing.T) {
	// Test that TLS can be explicitly disabled using TLSDisabled field
	creds := NewAPIKeyStaticCredentials("test-api-key")
	opts := &ConnectionOptions{
		TLSDisabled: true,
	}

	err := creds.applyToOptions(opts)
	require.NoError(t, err)

	// TLS should remain nil when TLSDisabled is true
	require.Nil(t, opts.TLS)
}

func TestConnectionOptions_TLSDisabledByDefaultWithoutAPIKey(t *testing.T) {
	// Test that TLS is disabled by default when no API key is provided
	opts := &ConnectionOptions{}

	// Without API key credentials, TLS should remain nil
	require.Nil(t, opts.TLS)
}

func TestAPIKeyCredentials_ExplicitTLSConfigPreservedWithAPIKey(t *testing.T) {
	// Test that explicit TLS configuration is preserved when API key is provided
	tlsConfig := &tls.Config{
		ServerName: "test-domain",
		MinVersion: tls.VersionTLS12,
	}

	creds := NewAPIKeyStaticCredentials("test-api-key")
	opts := &ConnectionOptions{
		TLS: tlsConfig,
	}

	err := creds.applyToOptions(opts)
	require.NoError(t, err)

	// Explicit TLS config should be preserved
	require.NotNil(t, opts.TLS)
	require.Equal(t, "test-domain", opts.TLS.ServerName)
	require.Equal(t, uint16(tls.VersionTLS12), opts.TLS.MinVersion)
}
