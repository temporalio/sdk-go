package test_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	operatorservice "go.temporal.io/api/operatorservice/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// intTestSigningCodec adds a context-derived signature on Encode and verifies on Decode.
type intTestSigningCodec struct {
	signature string
}

func (c *intTestSigningCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	switch sc := ctx.(type) {
	case converter.WorkflowSerializationContext:
		return &intTestSigningCodec{signature: sc.WorkflowID}
	case converter.ActivitySerializationContext:
		return &intTestSigningCodec{signature: sc.WorkflowID + ":" + sc.ActivityType}
	}
	return c
}

func (c *intTestSigningCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		clone := proto.Clone(p).(*commonpb.Payload)
		if clone.Metadata == nil {
			clone.Metadata = map[string][]byte{}
		}
		clone.Metadata["ctx-signature"] = []byte(c.signature)
		result[i] = clone
	}
	return result, nil
}

func (c *intTestSigningCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		sig := string(p.Metadata["ctx-signature"])
		if sig != c.signature {
			return nil, fmt.Errorf("signature mismatch: got %q, want %q", sig, c.signature)
		}
		clone := proto.Clone(p).(*commonpb.Payload)
		delete(clone.Metadata, "ctx-signature")
		result[i] = clone
	}
	return result, nil
}

func intTestToUpperActivity(_ context.Context, input string) (string, error) {
	return strings.ToUpper(input), nil
}

func intTestChildWorkflow(ctx workflow.Context, input string) (string, error) {
	return "child:" + input, nil
}

func intTestCombinedWorkflow(ctx workflow.Context, input string) (string, error) {
	// Side effect
	var sideEffectVal string
	err := workflow.SideEffect(ctx, func(ctx workflow.Context) any {
		return "side"
	}).Get(&sideEffectVal)
	if err != nil {
		return "", err
	}

	// Activity
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	var actResult string
	err = workflow.ExecuteActivity(actCtx, intTestToUpperActivity, input).Get(ctx, &actResult)
	if err != nil {
		return "", err
	}

	// Child workflow
	cwo := workflow.ChildWorkflowOptions{WorkflowRunTimeout: time.Minute}
	childCtx := workflow.WithChildOptions(ctx, cwo)
	var childResult string
	err = workflow.ExecuteChildWorkflow(childCtx, intTestChildWorkflow, actResult).Get(ctx, &childResult)
	if err != nil {
		return "", err
	}

	return sideEffectVal + "|" + childResult, nil
}

// newSerCtxClientAndWorker creates a client and worker both using a signing codec CDC.
func (ts *IntegrationTestSuite) newSerCtxClientAndWorker(taskQueue string) (client.Client, worker.Worker) {
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &intTestSigningCodec{})

	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = codecDC
	})
	ts.NoError(err)

	w := worker.New(c, taskQueue, worker.Options{})
	return c, w
}

func (ts *IntegrationTestSuite) TestSerializationContext_EndToEnd() {
	taskQueue := "test-ser-ctx-combined-" + ts.T().Name()
	c, w := ts.newSerCtxClientAndWorker(taskQueue)
	defer c.Close()

	w.RegisterWorkflow(intTestCombinedWorkflow)
	w.RegisterWorkflow(intTestChildWorkflow)
	w.RegisterActivity(intTestToUpperActivity)
	ts.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "ser-ctx-combined-" + ts.T().Name(),
		TaskQueue: taskQueue,
	}, intTestCombinedWorkflow, "hello")
	ts.NoError(err)

	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.Equal("side|child:HELLO", result)
}

type intTestNexusInput struct {
	Value string
	Fail  bool
}

var intTestNexusOperation = nexus.NewSyncOperation(
	"serialization-context-operation",
	func(_ context.Context, input intTestNexusInput, _ nexus.StartOperationOptions) (string, error) {
		if input.Fail {
			return "", &nexus.OperationError{
				State:   nexus.OperationStateFailed,
				Message: "expected Nexus failure",
				Cause:   fmt.Errorf("expected Nexus failure cause"),
			}
		}
		return input.Value, nil
	},
)

type intTestNexusCodec struct {
	key string
}

func (c *intTestNexusCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	switch sc := ctx.(type) {
	case converter.WorkflowSerializationContext:
		return &intTestNexusCodec{key: "workflow:" + sc.WorkflowID}
	case converter.NexusSerializationContext:
		return &intTestNexusCodec{key: "endpoint:" + sc.Endpoint}
	default:
		return c
	}
}

func (c *intTestNexusCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		result[i] = proto.Clone(payload).(*commonpb.Payload)
		if result[i].Metadata == nil {
			result[i].Metadata = make(map[string][]byte)
		}
		result[i].Metadata["nexus-endpoint-key"] = []byte(c.key)
	}
	return result, nil
}

func (c *intTestNexusCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		key := string(payload.Metadata["nexus-endpoint-key"])
		if key != c.key {
			return nil, fmt.Errorf("Nexus endpoint key mismatch: got %q, want %q", key, c.key)
		}
		result[i] = proto.Clone(payload).(*commonpb.Payload)
		delete(result[i].Metadata, "nexus-endpoint-key")
	}
	return result, nil
}

type intTestNexusFailureConverter struct {
	converter.FailureConverter
	key string
}

func (c *intTestNexusFailureConverter) WithSerializationContext(
	ctx converter.SerializationContext,
) converter.FailureConverter {
	if nexusCtx, ok := ctx.(converter.NexusSerializationContext); ok {
		return &intTestNexusFailureConverter{
			FailureConverter: c.FailureConverter,
			key:              "endpoint:" + nexusCtx.Endpoint,
		}
	}
	return c
}

func (c *intTestNexusFailureConverter) ErrorToFailure(err error) *failurepb.Failure {
	failure := c.FailureConverter.ErrorToFailure(err)
	if failure == nil || c.key == "" {
		return failure
	}
	failure = proto.Clone(failure).(*failurepb.Failure)
	failure.Source = c.key
	return failure
}

func (c *intTestNexusFailureConverter) FailureToError(failure *failurepb.Failure) error {
	if c.key == "" {
		return c.FailureConverter.FailureToError(failure)
	}
	for current := failure; current != nil; current = current.Cause {
		if current.Source == c.key {
			return c.FailureConverter.FailureToError(failure)
		}
	}
	return fmt.Errorf("Nexus failure key mismatch: missing %q", c.key)
}

func intTestNexusCallerWorkflow(ctx workflow.Context, endpoints []string) (string, error) {
	first := workflow.NewNexusClient(endpoints[0], "serialization-context-service").ExecuteOperation(
		ctx,
		intTestNexusOperation,
		intTestNexusInput{Value: "first"},
		workflow.NexusOperationOptions{},
	)
	second := workflow.NewNexusClient(endpoints[1], "serialization-context-service").ExecuteOperation(
		ctx,
		intTestNexusOperation.Name(),
		intTestNexusInput{Value: "second"},
		workflow.NexusOperationOptions{},
	)

	var results []string
	var operationErr error
	selector := workflow.NewSelector(ctx)
	for _, future := range []workflow.Future{first, second} {
		selector.AddFuture(future, func(f workflow.Future) {
			var result string
			if err := f.Get(ctx, &result); err != nil {
				operationErr = err
				return
			}
			results = append(results, result)
		})
	}
	for len(results) < 2 && operationErr == nil {
		selector.Select(ctx)
	}
	return strings.Join(results, "|"), operationErr
}

func intTestNexusFailureWorkflow(ctx workflow.Context, endpoint string) error {
	return workflow.NewNexusClient(endpoint, "serialization-context-service").ExecuteOperation(
		ctx,
		intTestNexusOperation,
		intTestNexusInput{Fail: true},
		workflow.NexusOperationOptions{},
	).Get(ctx, nil)
}

func (ts *IntegrationTestSuite) TestSerializationContext_NexusCallerEndpointIsolation() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	callerDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &intTestNexusCodec{})
	callerClient, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = callerDC
		options.FailureConverter = &intTestNexusFailureConverter{
			FailureConverter: temporal.GetDefaultFailureConverter(),
		}
	})
	ts.NoError(err)
	defer callerClient.Close()

	endpointNames := []string{"nexus-ser-ctx-a-" + uuid.NewString(), "nexus-ser-ctx-b-" + uuid.NewString()}
	handlerTaskQueues := []string{"nexus-ser-ctx-handler-a-" + uuid.NewString(), "nexus-ser-ctx-handler-b-" + uuid.NewString()}
	for i := range endpointNames {
		response, err := callerClient.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
			Spec: &nexuspb.EndpointSpec{
				Name: endpointNames[i],
				Target: &nexuspb.EndpointTarget{Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: ts.config.Namespace,
						TaskQueue: handlerTaskQueues[i],
					},
				}},
			},
		})
		ts.NoError(err)
		defer func(endpoint *nexuspb.Endpoint) {
			_, _ = callerClient.OperatorService().DeleteNexusEndpoint(ctx, &operatorservice.DeleteNexusEndpointRequest{
				Id: endpoint.Id, Version: endpoint.Version,
			})
		}(response.Endpoint)
	}

	var handlerWorkers []worker.Worker
	for i := range endpointNames {
		handlerDC := converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(),
			&intTestNexusCodec{key: "endpoint:" + endpointNames[i]},
		)
		handlerClient, err := ts.newDefaultClient(func(options *client.Options) {
			options.DataConverter = handlerDC
			options.FailureConverter = &intTestNexusFailureConverter{
				FailureConverter: temporal.GetDefaultFailureConverter(),
				key:              "endpoint:" + endpointNames[i],
			}
		})
		ts.NoError(err)
		defer handlerClient.Close()

		handlerWorker := worker.New(handlerClient, handlerTaskQueues[i], worker.Options{
			DisableWorkflowWorker: true,
		})
		service := nexus.NewService("serialization-context-service")
		ts.NoError(service.Register(intTestNexusOperation))
		handlerWorker.RegisterNexusService(service)
		ts.NoError(handlerWorker.Start())
		handlerWorkers = append(handlerWorkers, handlerWorker)
	}
	defer func() {
		for _, handlerWorker := range handlerWorkers {
			handlerWorker.Stop()
		}
	}()

	callerTaskQueue := "nexus-ser-ctx-caller-" + uuid.NewString()
	callerWorker := worker.New(callerClient, callerTaskQueue, worker.Options{})
	callerWorker.RegisterWorkflow(intTestNexusCallerWorkflow)
	callerWorker.RegisterWorkflow(intTestNexusFailureWorkflow)
	ts.NoError(callerWorker.Start())
	defer callerWorker.Stop()

	run, err := callerClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "nexus-ser-ctx-caller-" + uuid.NewString(),
		TaskQueue: callerTaskQueue,
	}, intTestNexusCallerWorkflow, endpointNames)
	ts.NoError(err)
	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.ElementsMatch([]string{"first", "second"}, strings.Split(result, "|"))

	failureRun, err := callerClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "nexus-ser-ctx-failure-" + uuid.NewString(),
		TaskQueue: callerTaskQueue,
	}, intTestNexusFailureWorkflow, endpointNames[1])
	ts.NoError(err)
	err = failureRun.Get(ctx, nil)
	ts.ErrorContains(err, "expected Nexus failure")
	ts.NotContains(err.Error(), "Nexus failure key mismatch")
}
