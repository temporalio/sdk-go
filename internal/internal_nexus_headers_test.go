package internal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/converter"
)

func TestNexusReservedHeaderKeysAreNamespaced(t *testing.T) {
	t.Parallel()
	for _, key := range []string{NexusEndpointHeaderKey, NexusServiceHeaderKey, NexusOperationHeaderKey} {
		require.True(t, strings.HasPrefix(key, "__temporal_"),
			"header key %q must start with __temporal_", key)
	}
}

func TestNewRawStringHeaderPayload_BinaryPlainEncoding(t *testing.T) {
	t.Parallel()
	p := NewRawStringHeaderPayload("my-endpoint")
	require.NotNil(t, p)
	require.Equal(t, []byte(converter.MetadataEncodingBinary), p.Metadata[converter.MetadataEncoding])
	require.Equal(t, []byte("my-endpoint"), p.Data)
}

func TestNewRawStringHeaderPayload_EmptyString(t *testing.T) {
	t.Parallel()
	p := NewRawStringHeaderPayload("")
	require.NotNil(t, p)
	require.Equal(t, []byte(""), p.Data)
	require.Equal(t, []byte(converter.MetadataEncodingBinary), p.Metadata[converter.MetadataEncoding])
}
