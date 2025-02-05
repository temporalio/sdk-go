package temporalnexus

import (
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeOperationToken(t *testing.T) {
	content := &nexus.Content{
		Header: nexus.Header{"a": "b"},
		Data:   []byte("abc"),
	}
	token, err := encodeOperationToken(content)
	require.NoError(t, err)
	decoded, err := decodeOperationToken(token)
	require.NoError(t, err)
	require.Equal(t, content, decoded)
}
