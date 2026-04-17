package internal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"

	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

func TestSystemNexusPayloadVisitor_VisitsNestedPayloadsOnly(t *testing.T) {
	storageParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("system-nexus")},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)

	req := systemnexus.SignalWithStartWorkflowExecutionRequest{
		Namespace:  "default",
		WorkflowID: "system-nexus-workflow-id",
		SignalName: "test-signal",
		Input: &systemnexus.Payloads{Payloads: []systemnexus.Payload{
			mustSystemNexusPayloadJSON(t, "workflow-input"),
		}},
		SignalInput: &systemnexus.Payloads{Payloads: []systemnexus.Payload{
			mustSystemNexusPayloadJSON(t, "signal-input"),
		}},
		Memo: &systemnexus.Memo{
			Fields: map[string]systemnexus.Payload{
				"memo-key": mustSystemNexusPayloadJSON(t, "memo-value"),
			},
		},
		Header: &systemnexus.Header{
			Fields: map[string]systemnexus.Payload{
				"header-key": mustSystemNexusPayloadJSON(t, "header-value"),
			},
		},
		UserMetadata: &systemnexus.UserMetadata{
			Summary: payloadPtr(mustSystemNexusPayloadJSON(t, "summary-value")),
			Details: payloadPtr(mustSystemNexusPayloadJSON(t, "details-value")),
		},
		SearchAttributes: &systemnexus.SearchAttributes{
			IndexedFields: map[string]systemnexus.Payload{
				"custom-key": mustSystemNexusPayloadJSON(t, "search-attribute-value"),
			},
		},
	}

	outerPayload, err := converter.GetDefaultDataConverter().ToPayload(req)
	require.NoError(t, err)

	attrs := &commandpb.ScheduleNexusOperationCommandAttributes{
		Service:   systemnexus.WorkflowService.ServiceName,
		Operation: systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name(),
		Input:     outerPayload,
	}
	err = visitProtoPayloads(context.Background(), NewExternalStorageVisitor(storageParams), attrs)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(attrs.Input, &decoded))
	requirePayloadJSONReference(t, decoded["input"], "payloads")
	requirePayloadJSONReference(t, decoded["signalInput"], "payloads")
	requirePayloadJSONReference(t, decoded["memo"], "fields", "memo-key")
	requirePayloadJSONReference(t, decoded["header"], "fields", "header-key")
	requirePayloadJSONReference(t, decoded["userMetadata"], "summary")
	requirePayloadJSONReference(t, decoded["userMetadata"], "details")

	searchAttr := decoded["searchAttributes"].(map[string]any)["indexedFields"].(map[string]any)["custom-key"]
	requirePayloadJSONReference(t, searchAttr)

	driver := storageParams.driverMap["system-nexus"].(*testStorageDriver)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	require.Len(t, driver.data, 6)
}

func TestNewSystemNexusSignalWithStartInput_PreservesPreencodedPayloads(t *testing.T) {
	codec := &testSignalWithStartCodec{}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)

	input, err := encodeArgs(dc, []interface{}{"workflow-input"})
	require.NoError(t, err)
	signalInput, err := encodeArg(dc, "signal-input")
	require.NoError(t, err)
	memo, err := getWorkflowMemo(map[string]interface{}{"memo-key": "memo-value"}, dc, true)
	require.NoError(t, err)
	userMetadata, err := buildUserMetadata("summary-value", "details-value", dc)
	require.NoError(t, err)

	outerPayload, err := newSystemNexusSignalWithStartPayload(
		"default",
		"test-request-id",
		"system-nexus-workflow-id",
		"test-signal",
		&WorkflowType{Name: "test-workflow"},
		input,
		signalInput,
		nil,
		memo,
		nil,
		userMetadata,
		StartWorkflowOptions{TaskQueue: "task-queue"},
	)
	require.NoError(t, err)

	var decodedReq systemnexus.SignalWithStartWorkflowExecutionRequest
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(outerPayload, &decodedReq))
	require.Equal(t, "test-request-id", decodedReq.RequestID)

	handler := systemnexus.GetTemporalNexusPayloadVisitor(
		systemnexus.WorkflowService.ServiceName,
		systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name(),
	)
	require.NotNil(t, handler)

	value := handler.InputType()
	require.IsType(t, &systemnexus.SignalWithStartWorkflowExecutionRequest{}, value)
	require.NoError(t, json.Unmarshal(outerPayload.GetData(), value))

	var visitedPayloads []*commonpb.Payload
	visitedValue, err := handler.Visit(
		value,
		func(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
			visitedPayloads = append(visitedPayloads, payloads...)
			return payloads, nil
		},
		false,
	)
	require.NoError(t, err)
	require.IsType(t, &systemnexus.SignalWithStartWorkflowExecutionRequest{}, visitedValue)
	require.Len(t, visitedPayloads, 4)
	for _, payload := range visitedPayloads {
		require.Equal(t, []byte("true"), payload.GetMetadata()["test-codec"])
	}
}

func requirePayloadJSONReference(t *testing.T, value any, path ...string) {
	t.Helper()
	current := value
	for _, segment := range path {
		next, ok := current.(map[string]any)
		require.True(t, ok)
		current = next[segment]
	}
	switch typed := current.(type) {
	case []any:
		require.NotEmpty(t, typed)
		requirePayloadJSONReference(t, typed[0])
	case map[string]any:
		_, hasExternalPayloads := typed["externalPayloads"]
		_, hasData := typed["data"]
		require.True(t, hasExternalPayloads || hasData)
	default:
		require.Failf(t, "expected rewritten payload JSON", "got %T", current)
	}
}

type testSignalWithStartCodec struct{}

func payloadPtr(payload systemnexus.Payload) *systemnexus.Payload {
	return &payload
}

func mustSystemNexusPayloadJSON(t *testing.T, value interface{}) systemnexus.Payload {
	t.Helper()
	payload, err := converter.GetDefaultDataConverter().ToPayload(value)
	require.NoError(t, err)
	result, err := toSystemNexusPayload(payload)
	require.NoError(t, err)
	return result
}

func (c *testSignalWithStartCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	encoded := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		cloned := proto.Clone(payload).(*commonpb.Payload)
		if cloned.Metadata == nil {
			cloned.Metadata = make(map[string][]byte, 1)
		}
		cloned.Metadata["test-codec"] = []byte("true")
		encoded[i] = cloned
	}
	return encoded, nil
}

func (c *testSignalWithStartCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}
