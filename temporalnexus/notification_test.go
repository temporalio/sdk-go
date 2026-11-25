package temporalnexus_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	notificationpb "go.temporal.io/api/notificationservice/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/temporalnexus"
)

func TestNotificationModels(t *testing.T) {
	// Matches the binary protobuf converter that workers use for System Nexus envelopes.
	systemDC := internal.MakeTransferAware(converter.NewCompositeDataConverter(converter.NewProtoPayloadConverter()))
	userDC := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}),
	)
	dc := commonconverter.WithDataConverterSystemNexusContext(systemDC, userDC)
	sourceContext := "source"

	encodeRequest := func(t *testing.T, request any) (*commonpb.Payload, *notificationpb.OnCompleteRequest) {
		t.Helper()
		payload, err := dc.ToPayload(request)
		require.NoError(t, err)
		require.Equal(t, converter.MetadataEncodingProto, string(payload.GetMetadata()[converter.MetadataEncoding]))
		require.Equal(t, "true", string(payload.GetMetadata()[proxy.SystemPayloadMetadataKey]))
		require.Equal(t, "temporal.api.notificationservice.v1.OnCompleteRequest", string(payload.GetMetadata()[converter.MetadataMessageType]))
		// Workers reject invalid System Nexus envelopes during payload visitation.
		err = proxy.VisitPayloads(t.Context(), &commonpb.Payloads{Payloads: []*commonpb.Payload{payload}}, proxy.VisitPayloadsOptions{
			Visitor: func(_ *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return payloads, nil
			},
		})
		require.NoError(t, err)
		var wire notificationpb.OnCompleteRequest
		require.NoError(t, systemDC.FromPayload(payload, &wire))
		return payload, &wire
	}

	t.Run("success", func(t *testing.T) {
		var result temporalnexus.OnCompleteRequestResult[int] = &temporalnexus.OnCompleteRequestResultSuccess[int]{Value: 42}
		request := temporalnexus.OnCompleteRequest[int, string]{
			Result:        result,
			SourceContext: &sourceContext,
		}
		payload, wire := encodeRequest(t, request)
		require.Equal(t, "binary/zlib", string(wire.GetSuccess().GetMetadata()[converter.MetadataEncoding]))
		require.Equal(t, "binary/zlib", string(wire.GetSourceContext().GetMetadata()[converter.MetadataEncoding]))

		var decoded temporalnexus.OnCompleteRequest[int, string]
		require.NoError(t, dc.FromPayload(payload, &decoded))
		require.Equal(t, temporalnexus.OnCompleteRequestResultSuccess[int]{Value: 42}, decoded.Result)
		require.Equal(t, request.SourceContext, decoded.SourceContext)
	})

	t.Run("failure", func(t *testing.T) {
		request := temporalnexus.OnCompleteRequest[int, string]{
			Result:        temporalnexus.OnCompleteRequestResultFailure[int]{Value: temporal.NewApplicationError("operation failed", "CompletionFailure")},
			SourceContext: &sourceContext,
		}
		payload, wire := encodeRequest(t, request)
		require.Equal(t, "operation failed", wire.GetFailure().GetMessage())

		var decoded temporalnexus.OnCompleteRequest[int, string]
		require.NoError(t, dc.FromPayload(payload, &decoded))
		failure, ok := decoded.Result.(temporalnexus.OnCompleteRequestResultFailure[int])
		require.True(t, ok)
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, failure.Value, &appErr)
		require.Equal(t, "CompletionFailure", appErr.Type())
		require.Equal(t, request.SourceContext, decoded.SourceContext)
	})

	t.Run("no source context", func(t *testing.T) {
		request := temporalnexus.OnCompleteRequest[int, string]{
			Result: temporalnexus.OnCompleteRequestResultSuccess[int]{Value: 42},
		}
		payload, wire := encodeRequest(t, request)
		require.Nil(t, wire.GetSourceContext())

		var decoded temporalnexus.OnCompleteRequest[int, string]
		require.NoError(t, dc.FromPayload(payload, &decoded))
		require.Equal(t, request, decoded)
	})

	t.Run("acknowledgement", func(t *testing.T) {
		payload, err := dc.ToPayload(temporalnexus.OnCompleteResponse{})
		require.NoError(t, err)
		var wire notificationpb.OnCompleteResponse
		require.NoError(t, systemDC.FromPayload(payload, &wire))
		var response temporalnexus.OnCompleteResponse
		require.NoError(t, dc.FromPayload(payload, &response))
	})
}
