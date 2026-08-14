package internal

import (
	"errors"
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"

	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
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

	nonRetryableValidationErr := NewApplicationErrorWithOptions(
		"invalid payload",
		payloadValidationErrorType,
		ApplicationErrorOptions{NonRetryable: true},
	)
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
				require.ErrorContains(t, handlerErr.Cause, "invalid payload")
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
			serializer := &payloadSerializer{
				converter: failingDataConverter{
					DataConverter: converter.GetDefaultDataConverter(),
					err:           tt.err,
				},
				payload: payload,
			}
			var v string
			tt.check(t, serializer.Deserialize(nil, &v))
		})
	}
}

func TestPayloadSerializerDeserializeSuccess(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload("input")
	require.NoError(t, err)

	serializer := &payloadSerializer{
		converter: converter.GetDefaultDataConverter(),
		payload:   payload,
	}
	var v string
	require.NoError(t, serializer.Deserialize(nil, &v))
	require.Equal(t, "input", v)
}
