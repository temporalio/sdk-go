package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	sdkpb "go.temporal.io/api/sdk/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	systemnexus "go.temporal.io/sdk/temporalnexus/system"
	"google.golang.org/protobuf/proto"
)

func TestSystemNexusOutboundPayloadVisitor_RewritesNestedPayloadsOnly(t *testing.T) {
	codec := &testRejectOuterSystemNexusCodec{}
	storageParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("system-nexus")},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)

	req := &workflowservicepb.SignalWithStartWorkflowExecutionRequest{
		Namespace:  "default",
		WorkflowId: "system-nexus-workflow-id",
		SignalName: "test-signal",
		Input: &commonpb.Payloads{Payloads: []*commonpb.Payload{
			testJSONStringPayload("workflow-input"),
		}},
		SignalInput: &commonpb.Payloads{Payloads: []*commonpb.Payload{
			testJSONStringPayload("signal-input"),
		}},
		Memo: &commonpb.Memo{
			Fields: map[string]*commonpb.Payload{
				"memo-key": testJSONStringPayload("memo-value"),
			},
		},
		Header: &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"header-key": testJSONStringPayload("header-value"),
			},
		},
		UserMetadata: &sdkpb.UserMetadata{
			Summary: testJSONStringPayload("summary-value"),
			Details: testJSONStringPayload("details-value"),
		},
		SearchAttributes: &commonpb.SearchAttributes{
			IndexedFields: map[string]*commonpb.Payload{
				"custom-key": testJSONStringPayload("search-attribute-value"),
			},
		},
	}

	outerPayload, err := getSystemNexusPayloadConverter().ToPayload(req)
	require.NoError(t, err)

	visitor := newSystemNexusOutboundPayloadVisitor(
		converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec),
		NewExternalStorageVisitor(storageParams),
	)

	rewritten, err := visitor.Visit(&proxy.VisitPayloadsContext{
		Context:               context.Background(),
		Parent:                &commandpb.ScheduleNexusOperationCommandAttributes{Service: systemnexus.WorkflowService.ServiceName, Operation: systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name()},
		SinglePayloadRequired: true,
	}, []*commonpb.Payload{outerPayload})
	require.NoError(t, err)
	require.Len(t, rewritten, 1)

	decoded := &workflowservicepb.SignalWithStartWorkflowExecutionRequest{}
	require.NoError(t, getSystemNexusPayloadConverter().FromPayload(rewritten[0], decoded))

	for _, payload := range decoded.GetInput().GetPayloads() {
		require.NotEmpty(t, payload.GetExternalPayloads())
	}
	for _, payload := range decoded.GetSignalInput().GetPayloads() {
		require.NotEmpty(t, payload.GetExternalPayloads())
	}
	require.NotEmpty(t, decoded.GetMemo().GetFields()["memo-key"].GetExternalPayloads())
	require.NotEmpty(t, decoded.GetHeader().GetFields()["header-key"].GetExternalPayloads())
	require.NotEmpty(t, decoded.GetUserMetadata().GetSummary().GetExternalPayloads())
	require.NotEmpty(t, decoded.GetUserMetadata().GetDetails().GetExternalPayloads())

	searchAttr := decoded.GetSearchAttributes().GetIndexedFields()["custom-key"]
	require.Empty(t, searchAttr.GetExternalPayloads())
	require.NotContains(t, searchAttr.GetMetadata(), "test-codec")
	require.GreaterOrEqual(t, codec.EncodeCount(), 6)

	driver := storageParams.driverMap["system-nexus"].(*testStorageDriver)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	require.NotEmpty(t, driver.data)
	for _, payload := range driver.data {
		require.Equal(t, []byte("true"), payload.GetMetadata()["test-codec"])
	}
}

func testJSONStringPayload(value string) *commonpb.Payload {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON),
		},
		Data: data,
	}
}

type testRejectOuterSystemNexusCodec struct {
	mu          sync.Mutex
	encodeCount int
}

func (c *testRejectOuterSystemNexusCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	encoded := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		if testLooksLikeSystemNexusEnvelope(payload) {
			return nil, fmt.Errorf("outer system nexus envelope should not be codec encoded")
		}
		cloned := proto.Clone(payload).(*commonpb.Payload)
		if cloned.Metadata == nil {
			cloned.Metadata = make(map[string][]byte, 1)
		}
		cloned.Metadata["test-codec"] = []byte("true")
		encoded[i] = cloned
	}

	c.mu.Lock()
	c.encodeCount += len(payloads)
	c.mu.Unlock()
	return encoded, nil
}

func (c *testRejectOuterSystemNexusCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	for _, payload := range payloads {
		if testLooksLikeSystemNexusEnvelope(payload) {
			return nil, fmt.Errorf("outer system nexus envelope should not be codec decoded")
		}
	}
	return payloads, nil
}

func (c *testRejectOuterSystemNexusCodec) EncodeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encodeCount
}

func testLooksLikeSystemNexusEnvelope(payload *commonpb.Payload) bool {
	var value map[string]any
	if err := json.Unmarshal(payload.GetData(), &value); err != nil {
		return false
	}
	_, hasNamespace := value["namespace"]
	_, hasWorkflowID := value["workflowId"]
	_, hasSignalName := value["signalName"]
	return hasNamespace && hasWorkflowID && hasSignalName
}
