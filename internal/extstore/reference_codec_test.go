package extstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	sdkpb "go.temporal.io/api/sdk/v1"
	"google.golang.org/protobuf/proto"
)

// wrapCodec serializes each payload into a new payload with its own encoding,
// dropping the original metadata like most real codecs. Decode passes through
// payloads it did not encode.
type wrapCodec struct {
	encodeCount int
	decodeCount int
}

const wrapCodecEncoding = "binary/wrapped"

func (c *wrapCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	c.encodeCount += len(payloads)
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, err
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{metadataEncoding: []byte(wrapCodecEncoding)},
			Data:     data,
		}
	}
	return result, nil
}

func (c *wrapCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	c.decodeCount += len(payloads)
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.Metadata[metadataEncoding]) != wrapCodecEncoding {
			result[i] = p
			continue
		}
		result[i] = &commonpb.Payload{}
		if err := proto.Unmarshal(p.Data, result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// passthroughCodec leaves payloads unchanged, like a codec that skips small
// payloads.
type passthroughCodec struct{}

func (passthroughCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}

func (passthroughCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}

func codecProvider(codec ReferenceCodec) ReferenceCodecProvider {
	return func(context.Context) ReferenceCodec { return codec }
}

func storageParams(t *testing.T) StorageParameters {
	t.Helper()
	params, err := ExternalStorageToParams(ExternalStorage{Drivers: []StorageDriver{newTestDriver("d")}})
	require.NoError(t, err)
	return params
}

func makeReference(t *testing.T, sizeBytes int64) *commonpb.Payload {
	t.Helper()
	ref, err := storageReferenceToPayload(&sdkpb.ExternalStorageReference{
		DriverName: "d",
		ClaimData:  map[string]string{"key": "k"},
	}, sizeBytes)
	require.NoError(t, err)
	return ref
}

func TestStorageReferencePayload_MarkedOnlyWhenWritingEncodedReferences(t *testing.T) {
	ref := makeReference(t, 512)
	require.Equal(t, WriteEncodedReferences, HasReferenceMarker(ref))
	require.True(t, IsStorageReference(ref))
}

func TestReferenceEncodingVisitor_EncodesOnlyReferences(t *testing.T) {
	codec := &wrapCodec{}
	visitor := NewReferenceEncodingVisitor(codecProvider(codec))
	inline := makePayload(t, "inline")
	ref := makeReference(t, 512)

	result, err := visitPayloads(t.Context(), visitor, []*commonpb.Payload{inline, ref})
	require.NoError(t, err)

	require.True(t, proto.Equal(inline, result[0]))
	require.Equal(t, 1, codec.encodeCount)
	require.Equal(t, wrapCodecEncoding, string(result[1].Metadata[metadataEncoding]))
	require.True(t, HasReferenceMarker(result[1]))
	require.Len(t, result[1].ExternalPayloads, 1)
	require.Equal(t, int64(512), result[1].ExternalPayloads[0].SizeBytes)
}

func TestReferenceEncodingVisitor_SkipsHeaders(t *testing.T) {
	codec := &wrapCodec{}
	visitor := NewReferenceEncodingVisitor(codecProvider(codec))
	ref := makeReference(t, 512)

	vpc := &proxy.VisitPayloadsContext{Context: t.Context(), Parent: &commonpb.Header{}}
	result, err := visitor.Visit(vpc, []*commonpb.Payload{ref})
	require.NoError(t, err)

	require.True(t, proto.Equal(ref, result[0]))
	require.Equal(t, 0, codec.encodeCount)
}

func TestReferenceDecodingVisitor_SkipsHeaders(t *testing.T) {
	codec := &wrapCodec{}
	ref := makeReference(t, 512)
	ref.Metadata[metadataReferenceMarker] = []byte("true")

	vpc := &proxy.VisitPayloadsContext{Context: t.Context(), Parent: &commonpb.Header{}}
	result, err := NewReferenceDecodingVisitor(storageParams(t), codecProvider(codec)).Visit(vpc, []*commonpb.Payload{ref})
	require.NoError(t, err)
	require.True(t, proto.Equal(ref, result[0]))
	require.Equal(t, 0, codec.decodeCount)
}

func TestReferenceDecodingVisitor_RoundTrip(t *testing.T) {
	codec := &wrapCodec{}
	ref := makeReference(t, 512)
	encoded, err := visitPayloads(t.Context(), NewReferenceEncodingVisitor(codecProvider(codec)), []*commonpb.Payload{ref})
	require.NoError(t, err)

	decoded, err := visitPayloads(t.Context(), NewReferenceDecodingVisitor(storageParams(t), codecProvider(codec)), encoded)
	require.NoError(t, err)
	require.True(t, proto.Equal(ref, decoded[0]))
}

func TestReferenceDecodingVisitor_UnmarkedPayloadsUntouched(t *testing.T) {
	codec := &wrapCodec{}
	ref := makeReference(t, 512)
	delete(ref.Metadata, metadataReferenceMarker)
	inline := makePayload(t, "inline")

	result, err := visitPayloads(t.Context(), NewReferenceDecodingVisitor(storageParams(t), codecProvider(codec)), []*commonpb.Payload{ref, inline})
	require.NoError(t, err)

	require.True(t, proto.Equal(ref, result[0]))
	require.True(t, proto.Equal(inline, result[1]))
	require.Equal(t, 0, codec.decodeCount)
}

func TestReferenceDecodingVisitor_MarkedUnencodedReference(t *testing.T) {
	// A reference written with no codec carries the marker and is already the
	// reference, so decoding it is a no-op.
	ref := makeReference(t, 512)
	ref.Metadata[metadataReferenceMarker] = []byte("true")

	decoded, err := visitPayloads(t.Context(), NewReferenceDecodingVisitor(storageParams(t), codecProvider(passthroughCodec{})), []*commonpb.Payload{ref})
	require.NoError(t, err)
	require.True(t, proto.Equal(ref, decoded[0]))
}

func TestReferenceDecodingVisitor_UndecodableReferenceFails(t *testing.T) {
	ref := makeReference(t, 512)
	encoded, err := visitPayloads(t.Context(), NewReferenceEncodingVisitor(codecProvider(&wrapCodec{})), []*commonpb.Payload{ref})
	require.NoError(t, err)

	_, err = visitPayloads(t.Context(), NewReferenceDecodingVisitor(storageParams(t), codecProvider(passthroughCodec{})), encoded)
	require.ErrorContains(t, err, "[TMPRL1106]")
}

func TestReferenceDecodingVisitor_StorageNotConfiguredFails(t *testing.T) {
	ref := makeReference(t, 512)
	encoded, err := visitPayloads(t.Context(), NewReferenceEncodingVisitor(codecProvider(&wrapCodec{})), []*commonpb.Payload{ref})
	require.NoError(t, err)

	codec := &wrapCodec{}
	_, err = visitPayloads(t.Context(), NewReferenceDecodingVisitor(StorageParameters{}, codecProvider(codec)), encoded)
	require.ErrorContains(t, err, "[TMPRL1105]")
	require.Equal(t, 0, codec.decodeCount)
}

func TestReferenceOnlyDecodingVisitor_DoesNotRequireStorage(t *testing.T) {
	codec := &wrapCodec{}
	ref := makeReference(t, 512)
	encoded, err := visitPayloads(t.Context(), NewReferenceEncodingVisitor(codecProvider(codec)), []*commonpb.Payload{ref})
	require.NoError(t, err)

	decoded, err := visitPayloads(t.Context(), NewReferenceOnlyDecodingVisitor(codecProvider(codec)), encoded)
	require.NoError(t, err)
	require.True(t, proto.Equal(ref, decoded[0]))
}
