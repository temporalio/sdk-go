package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
)

// contextRecordingCodec wraps payloads and records the serialization context it
// was given for each decode.
type contextRecordingCodec struct {
	sc      converter.SerializationContext
	decoded *[]converter.SerializationContext
}

func (c contextRecordingCodec) WithSerializationContext(sc converter.SerializationContext) converter.PayloadCodec {
	return contextRecordingCodec{sc: sc, decoded: c.decoded}
}

func (c contextRecordingCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, err
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte("binary/recorded")},
			Data:     data,
		}
	}
	return result, nil
}

func (c contextRecordingCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		*c.decoded = append(*c.decoded, c.sc)
		result[i] = &commonpb.Payload{}
		if err := proto.Unmarshal(p.Data, result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func TestBindReferenceSerializationContext_DecodesWithBoundContext(t *testing.T) {
	var decoded []converter.SerializationContext
	codec := contextRecordingCodec{decoded: &decoded}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)

	data, err := protojson.Marshal(&sdkpb.ExternalStorageReference{DriverName: "d", ClaimData: map[string]string{"key": "k"}})
	require.NoError(t, err)
	encoded, err := codec.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{
			converter.MetadataEncoding:    []byte("json/protobuf"),
			converter.MetadataMessageType: []byte("temporal.api.sdk.v1.ExternalStorageReference"),
		},
		Data: data,
	}})
	require.NoError(t, err)
	encoded[0].Metadata["__temporal_external_storage_reference"] = []byte("true")

	sc := converter.WorkflowSerializationContext{Namespace: "ns", WorkflowID: "wf"}
	visitor := bindReferenceSerializationContext(
		newCompositePayloadVisitor(extstore.NewReferenceOnlyDecodingVisitor(newReferenceCodecProvider(dc))),
		sc,
	)
	result, err := visitPayload(t.Context(), visitor, encoded[0])
	require.NoError(t, err)

	require.True(t, extstore.IsStorageReference(result))
	require.Equal(t, []converter.SerializationContext{sc}, decoded)
}
