package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestByteSliceConverter(t *testing.T) {
	bc := NewByteSlicePayloadConverter()

	assert.Equal(t, bc.Encoding(), MetadataEncodingBinary)
	payload, err := bc.ToPayload(nil)
	assert.Nil(t, err)
	assert.Nil(t, payload)

	b := []byte("hello world")
	payload, err = bc.ToPayload(b)
	require.NoError(t, err)
	assert.Equal(t, string(payload.Metadata[MetadataEncoding]), MetadataEncodingBinary)
	assert.Equal(t, payload.Data, b)

	var gotBytes []byte
	var gotInterface interface{}

	err = bc.FromPayload(payload, &gotBytes)
	require.NoError(t, err)
	assert.Equal(t, b, gotBytes)

	err = bc.FromPayload(payload, &gotInterface)
	require.NoError(t, err)
	assert.Equal(t, b, gotInterface)

	gotString := bc.ToString(payload)
	// base64 unpadded encodeing of "hello world"
	assert.Equal(t, "aGVsbG8gd29ybGQ", gotString)

	// error branches
	err = bc.FromPayload(payload, nil)
	assert.ErrorIs(t, err, ErrValuePtrIsNotPointer)

	var s string
	err = bc.FromPayload(payload, s)
	assert.ErrorIs(t, err, ErrValuePtrIsNotPointer)

	err = bc.FromPayload(payload, &s)
	assert.ErrorIs(t, err, ErrTypeIsNotByteSlice)
}
