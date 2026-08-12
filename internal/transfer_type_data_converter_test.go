package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func TestClientDataConverterAutomaticallyWrapsTransferTypes(t *testing.T) {
	parent := &recordingTransferDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	client := NewServiceClient(nil, nil, ClientOptions{DataConverter: parent})

	assertTransferTypeConverterIsWrapped(t, client.dataConverter, parent)
}

func TestWorkflowDataConverterAutomaticallyWrapsTransferTypes(t *testing.T) {
	parent := &recordingTransferDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	ctx := WithDataConverter(Background(), parent)

	assertTransferTypeConverterIsWrapped(t, getDataConverterFromWorkflowContext(ctx), parent)
}

func TestWorkflowTestEnvironmentDataConverterAutomaticallyWrapsTransferTypes(t *testing.T) {
	parent := &recordingTransferDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	env := (&WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.SetDataConverter(parent)

	assertTransferTypeConverterIsWrapped(t, env.impl.dataConverter, parent)
}

func TestWorkflowReplayerDataConverterAutomaticallyWrapsTransferTypes(t *testing.T) {
	parent := &recordingTransferDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{DataConverter: parent})
	require.NoError(t, err)

	assertTransferTypeConverterIsWrapped(t, replayer.dataConverter, parent)
}

func TestWorkerDataConverterAutomaticallyWrapsTransferTypes(t *testing.T) {
	parent := &recordingTransferDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	params := workerExecutionParameters{DataConverter: parent}
	ensureRequiredParams(&params)

	assertTransferTypeConverterIsWrapped(t, params.DataConverter, parent)
}

func TestDefaultFailureConverterAutomaticallyWrapsTransferTypes(t *testing.T) {
	parent := &recordingTransferDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	failureConverter := NewDefaultFailureConverter(DefaultFailureConverterOptions{DataConverter: parent})
	failure := failureConverter.ErrorToFailure(NewApplicationError(
		"message", "", false, nil, &clientTransferTypeValue{Value: "value"}))
	require.Equal(t, "value", parent.lastValue)

	err := failureConverter.FailureToError(failure)
	var applicationError *ApplicationError
	require.True(t, errors.As(err, &applicationError))
	var details clientTransferTypeValue
	require.NoError(t, applicationError.Details(&details))
	require.Equal(t, "value", details.Value)
}

func assertTransferTypeConverterIsWrapped(
	t *testing.T,
	dc converter.DataConverter,
	parent *recordingTransferDataConverter,
) {
	t.Helper()
	payload, err := dc.ToPayload(&clientTransferTypeValue{Value: "value"})
	require.NoError(t, err)
	require.Equal(t, "value", parent.lastValue)

	var decoded clientTransferTypeValue
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, "value", decoded.Value)
}

type recordingTransferDataConverter struct {
	converter.DataConverter
	lastValue any
}

func (dc *recordingTransferDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	dc.lastValue = value
	return dc.DataConverter.ToPayload(value)
}

func (dc *recordingTransferDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	if len(values) > 0 {
		dc.lastValue = values[0]
	}
	return dc.DataConverter.ToPayloads(values...)
}

type clientTransferTypeValue struct{ Value string }

func (*clientTransferTypeValue) TransferTypeConverter() converter.TransferTypeConverter {
	return clientTransferTypeConverter{}
}

type clientTransferTypeConverter struct{}

func (clientTransferTypeConverter) NewTransferType() any { return new(string) }

func (clientTransferTypeConverter) ToTransferType(value any) (any, error) {
	return value.(*clientTransferTypeValue).Value, nil
}

func (clientTransferTypeConverter) FromTransferType(value any) (any, error) {
	return &clientTransferTypeValue{Value: *value.(*string)}, nil
}
