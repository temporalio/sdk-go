package converter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

func TestLocalFileStoreDriver_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	driver := NewLocalFileStoreDriver(dir)

	require.Equal(t, "local-file-store", driver.Name())
	require.Equal(t, "local-file-store", driver.Type())

	payloads := []*commonpb.Payload{
		{Metadata: map[string][]byte{"encoding": []byte("plain/text")}, Data: []byte("hello world")},
		{Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: []byte(`{"key":"value"}`)},
	}

	storeCtx := StorageDriverStoreContext{Context: context.Background()}
	claims, err := driver.Store(storeCtx, payloads)
	require.NoError(t, err)
	require.Len(t, claims, 2)

	for _, c := range claims {
		require.Contains(t, c.ClaimData, "path")
	}

	retrieveCtx := StorageDriverRetrieveContext{Context: context.Background()}
	got, err := driver.Retrieve(retrieveCtx, claims)
	require.NoError(t, err)
	require.Len(t, got, 2)

	for i, p := range got {
		require.Equal(t, payloads[i].Metadata, p.Metadata)
		require.Equal(t, payloads[i].Data, p.Data)
	}
}

func TestLocalFileStoreDriver_RetrieveMissingPath(t *testing.T) {
	dir := t.TempDir()
	driver := NewLocalFileStoreDriver(dir)

	claims := []StorageDriverClaim{{ClaimData: map[string]string{}}}
	_, err := driver.Retrieve(StorageDriverRetrieveContext{Context: context.Background()}, claims)
	require.ErrorContains(t, err, "missing path")
}

func TestLocalFileStoreDriver_RetrieveFileNotFound(t *testing.T) {
	dir := t.TempDir()
	driver := NewLocalFileStoreDriver(dir)

	claims := []StorageDriverClaim{{ClaimData: map[string]string{"path": "/nonexistent/file"}}}
	_, err := driver.Retrieve(StorageDriverRetrieveContext{Context: context.Background()}, claims)
	require.Error(t, err)
}

func TestLocalFileStoreDriver_ContentAddressable(t *testing.T) {
	dir := t.TempDir()
	driver := NewLocalFileStoreDriver(dir)

	payload := []*commonpb.Payload{
		{Data: []byte("same content")},
	}

	storeCtx := StorageDriverStoreContext{Context: context.Background()}
	claims1, err := driver.Store(storeCtx, payload)
	require.NoError(t, err)

	claims2, err := driver.Store(storeCtx, payload)
	require.NoError(t, err)

	// Same content produces the same path (content-addressable).
	require.Equal(t, claims1[0].ClaimData["path"], claims2[0].ClaimData["path"])
}
