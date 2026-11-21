package internal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
)

// failingDataConverter fails FromPayload with a fixed error.
type failingDataConverter struct {
	converter.DataConverter
	err error
}

func (dc failingDataConverter) FromPayload(*commonpb.Payload, any) error {
	return dc.err
}

func TestPayloadSerializerDeserializeErrors(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload("input")
	require.NoError(t, err)
	systemPayload, err := commonconverter.WithDataConverterSystemNexusContext(
		systemNexusDataConverter,
		converter.GetDefaultDataConverter(),
	).ToPayload(systemNexusModel{Value: "input"})
	require.NoError(t, err)

	violations := []map[string]string{{"path": "field", "reason": "must be an integer"}}
	nonRetryableValidationErr := NewPayloadValidationError(violations)
	retryableValidationErr := NewApplicationErrorWithOptions(
		"transient validation failure",
		payloadValidationErrorType,
		ApplicationErrorOptions{},
	)
	nonRetryableOtherErr := NewApplicationErrorWithOptions(
		"other application error",
		"SomeOtherErrorType",
		ApplicationErrorOptions{NonRetryable: true},
	)
	handlerErr := &nexus.HandlerError{
		Type:  nexus.HandlerErrorTypeNotImplemented,
		Cause: errors.New("not implemented"),
	}
	plainErr := errors.New("plain error")

	tests := []struct {
		name string
		err  error
		// check verifies the error returned from Deserialize.
		check func(t *testing.T, got error)
	}{
		{
			name: "non-retryable payload validation error is a bad request",
			err:  nonRetryableValidationErr,
			check: func(t *testing.T, got error) {
				var handlerErr *nexus.HandlerError
				require.ErrorAs(t, got, &handlerErr)
				require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
				require.Equal(t, "invalid operation input", handlerErr.Message)
				require.ErrorIs(t, handlerErr.Cause, nonRetryableValidationErr)
				var appErr *ApplicationError
				require.ErrorAs(t, handlerErr.Cause, &appErr)
				require.Equal(t, payloadValidationErrorType, appErr.Type())
				require.True(t, appErr.NonRetryable())
				require.Equal(t, "Payload validation failed", appErr.Message())
				var gotViolations []map[string]string
				require.NoError(t, appErr.Details(&gotViolations))
				require.Equal(t, violations, gotViolations)
			},
		},
		{
			name: "retryable payload validation error is passed through",
			err:  retryableValidationErr,
			check: func(t *testing.T, got error) {
				require.Equal(t, retryableValidationErr, got)
			},
		},
		{
			name: "non-retryable application error of another type is passed through",
			err:  nonRetryableOtherErr,
			check: func(t *testing.T, got error) {
				require.Equal(t, nonRetryableOtherErr, got)
			},
		},
		{
			name: "handler error is passed through",
			err:  handlerErr,
			check: func(t *testing.T, got error) {
				require.Equal(t, handlerErr, got)
			},
		},
		{
			name: "plain error is a bad request",
			err:  plainErr,
			check: func(t *testing.T, got error) {
				var handlerErr *nexus.HandlerError
				require.ErrorAs(t, got, &handlerErr)
				require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
				require.Equal(t, "cannot deserialize operation input", handlerErr.Message)
				require.ErrorIs(t, handlerErr.Cause, plainErr)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, system := range []bool{false, true} {
				t.Run(fmt.Sprintf("system=%v", system), func(t *testing.T) {
					serializer := &payloadSerializer{
						converter: failingDataConverter{
							DataConverter: converter.GetDefaultDataConverter(),
							err:           tt.err,
						},
						payload: payload,
					}
					var target any = new(string)
					if system {
						serializer.payload = systemPayload
						target = new(systemNexusModel)
					}
					tt.check(t, serializer.Deserialize(nil, target))
				})
			}
		})
	}
}

func TestPayloadSerializerDeserializeSuccess(t *testing.T) {
	for _, marker := range []string{"", "false", "TRUE"} {
		t.Run("marker="+marker, func(t *testing.T) {
			payload, err := converter.GetDefaultDataConverter().ToPayload("input")
			require.NoError(t, err)
			if marker != "" {
				payload.Metadata[proxy.SystemPayloadMetadataKey] = []byte(marker)
			}
			serializer := &payloadSerializer{
				converter: converter.GetDefaultDataConverter(),
				payload:   payload,
			}
			var v string
			require.NoError(t, serializer.Deserialize(nil, &v))
			require.Equal(t, "input", v)
		})
	}
}

type nexusInputDataConverter struct {
	converter.DataConverter
	fromPayload func(*commonpb.Payload, any) error
}

func (dc nexusInputDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	return dc.fromPayload(payload, valuePtr)
}

func TestPayloadSerializerSystemPayloadUsesNestedConverter(t *testing.T) {
	request := systemnexus_signalWithStartWorkflowRequest{
		Workflow:   "workflow",
		ID:         "workflow-id",
		Namespace:  "namespace",
		TaskQueue:  "task-queue",
		Signal:     "signal",
		Args:       []any{"first", "second"},
		SignalArgs: []any{"signal-input"},
	}
	dc := converter.GetDefaultDataConverter()
	payload, err := commonconverter.WithDataConverterSystemNexusContext(systemNexusDataConverter, dc).ToPayload(request)
	require.NoError(t, err)
	original := proto.Clone(payload)
	decodeCount := 0
	serializer := payloadSerializer{
		payload: payload,
		converter: nexusInputDataConverter{
			DataConverter: dc,
			fromPayload: func(payload *commonpb.Payload, valuePtr any) error {
				require.NotContains(t, payload.Metadata, proxy.SystemPayloadMetadataKey)
				require.IsType(t, new(any), valuePtr)
				decodeCount++
				var value string
				if err := dc.FromPayload(payload, &value); err != nil {
					return err
				}
				*valuePtr.(*any) = "converted-" + value
				return nil
			},
		},
	}
	var result systemnexus_signalWithStartWorkflowRequest
	require.NoError(t, serializer.Deserialize(nil, &result))
	require.Equal(t, 3, decodeCount)
	request.Args = []any{"converted-first", "converted-second"}
	request.SignalArgs = []any{"converted-signal-input"}
	require.Equal(t, request, result)
	require.True(t, proto.Equal(original, payload), "deserialization must not modify the input envelope")
}

type nexusInputCodec struct {
	converter.PayloadCodec
	decode func([]*commonpb.Payload) ([]*commonpb.Payload, error)
}

func (c nexusInputCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return c.decode(payloads)
}

func TestPayloadSerializerSystemPayloadCodecReplacement(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	payload, err := commonconverter.WithDataConverterSystemNexusContext(systemNexusDataConverter, dc).
		ToPayload(systemNexusModel{Value: "input"})
	require.NoError(t, err)
	replacement, err := dc.ToPayload("decoded-input")
	require.NoError(t, err)
	decodeCount := 0
	serializer := payloadSerializer{
		payload: payload,
		converter: converter.NewCodecDataConverter(dc, nexusInputCodec{
			decode: func(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
				require.Len(t, payloads, 1)
				require.NotContains(t, payloads[0].Metadata, proxy.SystemPayloadMetadataKey)
				decodeCount++
				return []*commonpb.Payload{replacement}, nil
			},
		}),
	}
	var result systemNexusModel
	require.NoError(t, serializer.Deserialize(nil, &result))
	require.Equal(t, systemNexusModel{Value: "decoded-input"}, result)
	require.Equal(t, 1, decodeCount)
}

func TestPayloadSerializerSystemPayloadWithCodec(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), newSystemEnvelopeRejectingCodec())
	payload, err := commonconverter.WithDataConverterSystemNexusContext(systemNexusDataConverter, dc).
		ToPayload(systemNexusModel{Value: "input"})
	require.NoError(t, err)
	serializer := payloadSerializer{converter: dc, payload: payload}
	var result systemNexusModel
	require.NoError(t, serializer.Deserialize(nil, &result))
	require.Equal(t, systemNexusModel{Value: "input"}, result)
}

// The poller validates the envelope during payload visitation, including the message type. See
// TestNexusPollerInvalidSystemPayload. Without that visitation, Deserialize must still keep the envelope away from the
// user codec and report a bad request.
func TestPayloadSerializerInvalidSystemPayload(t *testing.T) {
	for _, test := range []struct {
		name        string
		messageType string
		encoding    string
		data        []byte
	}{
		{name: "invalid encoding", messageType: "temporal.api.common.v1.Payload", encoding: converter.MetadataEncodingJSON},
		{name: "malformed protobuf", messageType: "temporal.api.common.v1.Payload", encoding: converter.MetadataEncodingProto, data: []byte{0xff}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), nexusInputCodec{
				decode: func([]*commonpb.Payload) ([]*commonpb.Payload, error) {
					t.Fatal("invalid system payload must not reach the codec")
					return nil, nil
				},
			})
			serializer := payloadSerializer{
				converter: dc,
				payload: &commonpb.Payload{
					Metadata: map[string][]byte{
						proxy.SystemPayloadMetadataKey: []byte("true"),
						converter.MetadataEncoding:     []byte(test.encoding),
						converter.MetadataMessageType:  []byte(test.messageType),
					},
					Data: test.data,
				},
			}
			var result systemNexusModel
			var handlerErr *nexus.HandlerError
			require.ErrorAs(t, serializer.Deserialize(nil, &result), &handlerErr)
			require.Equal(t, nexus.HandlerErrorTypeBadRequest, handlerErr.Type)
		})
	}
}
