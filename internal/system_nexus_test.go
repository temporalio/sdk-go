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
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

func TestSystemNexusOutboundPayloadVisitor_RewritesNestedPayloadsOnly(t *testing.T) {
	codec := &testRejectOuterSystemNexusCodec{}
	storageParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("system-nexus")},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)

	req := systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{
		Namespace:  "default",
		WorkflowID: "system-nexus-workflow-id",
		SignalName: "test-signal",
		Input: &systemnexus.Input{Payloads: []any{
			"workflow-input",
		}},
		SignalInput: &systemnexus.Input{Payloads: []any{
			"signal-input",
		}},
		Memo: &systemnexus.Memo{
			Fields: map[string]any{
				"memo-key": "memo-value",
			},
		},
		Header: &systemnexus.Header{
			Fields: map[string]any{
				"header-key": "header-value",
			},
		},
		UserMetadata: &systemnexus.UserMetadata{
			Summary: "summary-value",
			Details: "details-value",
		},
		SearchAttributes: &systemnexus.SearchAttributes{
			IndexedFields: map[string]any{
				"custom-key": "search-attribute-value",
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

	var decoded map[string]any
	require.NoError(t, getSystemNexusPayloadConverter().FromPayload(rewritten[0], &decoded))
	requirePayloadJSONReference(t, decoded["input"], "payloads")
	requirePayloadJSONReference(t, decoded["signalInput"], "payloads")
	requirePayloadJSONReference(t, decoded["memo"], "fields", "memo-key")
	requirePayloadJSONReference(t, decoded["header"], "fields", "header-key")
	requirePayloadJSONReference(t, decoded["userMetadata"], "summary")
	requirePayloadJSONReference(t, decoded["userMetadata"], "details")

	searchAttr := decoded["searchAttributes"].(map[string]any)["indexedFields"].(map[string]any)["custom-key"]
	require.Equal(t, "search-attribute-value", searchAttr)
	require.GreaterOrEqual(t, codec.EncodeCount(), 6)

	driver := storageParams.driverMap["system-nexus"].(*testStorageDriver)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	require.NotEmpty(t, driver.data)
	for _, payload := range driver.data {
		require.Equal(t, []byte("true"), payload.GetMetadata()["test-codec"])
	}
}

func TestSystemNexusOutboundPayloadVisitor_UsesContextAwareConverterOverride(t *testing.T) {
	storageParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("system-nexus")},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)

	req := systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{
		Namespace:  "default",
		WorkflowID: "system-nexus-workflow-id",
		SignalName: "test-signal",
		Input: &systemnexus.Input{Payloads: []any{
			"test",
		}},
	}

	outerPayload, err := getSystemNexusPayloadConverter().ToPayload(req)
	require.NoError(t, err)

	contextAwareConverter := WithWorkflowContext(
		WithValue(Background(), ContextAwareDataConverterContextKey, "e"),
		NewContextAwareDataConverter(converter.GetDefaultDataConverter()),
	)
	visitor := newSystemNexusOutboundPayloadVisitor(
		converter.GetDefaultDataConverter(),
		NewExternalStorageVisitor(storageParams),
	)

	rewritten, err := visitor.Visit(&proxy.VisitPayloadsContext{
		Context: context.WithValue(
			context.Background(),
			systemNexusPayloadConverterContextKey,
			contextAwareConverter,
		),
		Parent: &commandpb.ScheduleNexusOperationCommandAttributes{
			Service:   systemnexus.WorkflowService.ServiceName,
			Operation: systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name(),
		},
		SinglePayloadRequired: true,
	}, []*commonpb.Payload{outerPayload})
	require.NoError(t, err)
	require.Len(t, rewritten, 1)

	var decoded map[string]any
	require.NoError(t, getSystemNexusPayloadConverter().FromPayload(rewritten[0], &decoded))

	driver := storageParams.driverMap["system-nexus"].(*testStorageDriver)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	require.Len(t, driver.data, 1)
	for _, payload := range driver.data {
		require.Equal(t, []byte(`"t?st"`), payload.GetData())
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
		require.True(t, hasExternalPayloads)
	default:
		require.Failf(t, "expected rewritten payload JSON", "got %T", current)
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
