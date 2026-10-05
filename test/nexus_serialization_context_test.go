package test_test

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

type nexusContextSigningCodec struct {
	signature string
}

func (c *nexusContextSigningCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	switch value := ctx.(type) {
	case converter.NexusSerializationContext:
		return &nexusContextSigningCodec{signature: "nexus:" + value.Endpoint + ":" + value.Service + ":" + value.Operation}
	case converter.WorkflowSerializationContext:
		return &nexusContextSigningCodec{signature: "workflow:" + value.WorkflowID}
	case converter.ActivitySerializationContext:
		return &nexusContextSigningCodec{signature: "activity:" + value.ActivityType}
	default:
		return c
	}
}

func (c *nexusContextSigningCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		copy := proto.Clone(payload).(*commonpb.Payload)
		if c.signature != "" {
			if copy.Metadata == nil {
				copy.Metadata = make(map[string][]byte)
			}
			copy.Metadata["serialization-context-signature"] = []byte(c.signature)
		}
		result[i] = copy
	}
	return result, nil
}

func (c *nexusContextSigningCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		got := string(payload.GetMetadata()["serialization-context-signature"])
		if got != c.signature {
			return nil, fmt.Errorf("serialization context mismatch: got %q, want %q", got, c.signature)
		}
		copy := proto.Clone(payload).(*commonpb.Payload)
		delete(copy.Metadata, "serialization-context-signature")
		result[i] = copy
	}
	return result, nil
}

func (ts *IntegrationTestSuite) TestNexusSerializationContextPropagation() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "Nexus serialization context test creates an endpoint through Operator Service")
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	endpoint := "nexus-serialization-context-" + uuid.NewString()
	created, err := ts.client.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpoint,
			Target: &nexuspb.EndpointTarget{Variant: &nexuspb.EndpointTarget_Worker_{
				Worker: &nexuspb.EndpointTarget_Worker{Namespace: ts.config.Namespace, TaskQueue: ts.taskQueueName},
			}},
		},
	})
	ts.Require().NoError(err)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cleanupCancel()
		_, err := ts.client.OperatorService().DeleteNexusEndpoint(cleanupCtx, &operatorservice.DeleteNexusEndpointRequest{
			Id: created.GetEndpoint().GetId(), Version: created.GetEndpoint().GetVersion(),
		})
		ts.Require().NoError(err)
	}()

	temporalOpEndpoint = endpoint
	for _, testCase := range []struct {
		operation string
		workflow  func(workflow.Context, string) (string, error)
	}{
		{operation: "async-typed-op", workflow: ts.workflows.TemporalOpAsyncTypedCaller},
		{operation: "async-activity-op", workflow: ts.workflows.TemporalOpAsyncActivityCaller},
	} {
		ts.Run(testCase.operation, func() {
			input := uuid.NewString()
			run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID: "caller-" + input, TaskQueue: ts.taskQueueName, WorkflowTaskTimeout: 10 * time.Second,
			}, testCase.workflow, input)
			ts.Require().NoError(err)
			var result string
			ts.Require().NoError(run.Get(ctx, &result))
			ts.Equal(input, result)
			var backingResult string
			if testCase.operation == "async-typed-op" {
				ts.Require().NoError(ts.client.GetWorkflow(ctx, input, "").Get(ctx, &backingResult))
				iter := ts.client.GetWorkflowHistory(ctx, input, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
				ts.Require().True(iter.HasNext())
				started, err := iter.Next()
				ts.Require().NoError(err)
				ts.Equal(&nexuspb.PropagatedSerializationContext{
					Endpoint: endpoint, Service: temporalOpServiceName, Operation: testCase.operation,
				}, started.GetWorkflowExecutionStartedEventAttributes().GetPropagatedNexusSerializationContext())
			} else {
				handle := ts.client.GetActivityHandle(client.GetActivityHandleOptions{ActivityID: "act-" + input})
				ts.Require().NoError(handle.Get(ctx, &backingResult))
			}
			ts.Equal(input, backingResult)
		})
	}
}
