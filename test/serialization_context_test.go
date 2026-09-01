package test_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	operatorservice "go.temporal.io/api/operatorservice/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
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
}

const (
	intTestNexusService       = "serialization-context-service"
	intTestNexusOperationName = "serialization-context-operation"
)

var intTestNexusOperation = nexus.NewSyncOperation(
	intTestNexusOperationName,
	func(_ context.Context, input intTestNexusInput, _ nexus.StartOperationOptions) (string, error) {
		return input.Value, nil
	},
)

// intTestNexusCodecSelector selects a codec using the endpoint, service, and
// resolved operation name. It passes through non-Nexus payloads unchanged.
type intTestNexusCodecSelector struct {
	codecs map[string]converter.PayloadCodec
	err    error
}

type intTestNexusHMACCodec struct {
	key []byte
}

const intTestNexusHMACEncoding = "binary/nexus-hmac-test"

func intTestNexusContextKey(endpoint, service, operation string) string {
	return "endpoint:" + endpoint + "|service:" + service + "|operation:" + operation
}

func (c *intTestNexusCodecSelector) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	sc, ok := ctx.(converter.NexusSerializationContext)
	if !ok {
		return c
	}
	key := intTestNexusContextKey(sc.Endpoint, sc.Service, sc.Operation)
	// Select payload codec based on the Nexus service, endpoint, and operation.
	if codec, ok := c.codecs[key]; ok {
		return codec
	}
	return &intTestNexusCodecSelector{err: fmt.Errorf("no Nexus payload codec configured for %q", key)}
}

func (c *intTestNexusCodecSelector) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if c.err != nil {
		return nil, c.err
	}
	return payloads, nil
}

func (c *intTestNexusCodecSelector) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if c.err != nil {
		return nil, c.err
	}
	return payloads, nil
}

func (c *intTestNexusHMACCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		payloadBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal Nexus payload: %w", err)
		}
		mac := hmac.New(sha256.New, c.key)
		_, _ = mac.Write(payloadBytes)
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(intTestNexusHMACEncoding)},
			Data:     append(mac.Sum(nil), payloadBytes...),
		}
	}
	return result, nil
}

func (c *intTestNexusHMACCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, payload := range payloads {
		encoding := string(payload.Metadata[converter.MetadataEncoding])
		if encoding != intTestNexusHMACEncoding {
			return nil, fmt.Errorf("Nexus payload encoding mismatch: got %q, want %q", encoding, intTestNexusHMACEncoding)
		}
		if len(payload.Data) < sha256.Size {
			return nil, fmt.Errorf("Nexus HMAC payload is too short: got %d bytes", len(payload.Data))
		}
		signature, payloadBytes := payload.Data[:sha256.Size], payload.Data[sha256.Size:]
		mac := hmac.New(sha256.New, c.key)
		_, _ = mac.Write(payloadBytes)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return nil, errors.New("Nexus payload HMAC mismatch")
		}
		result[i] = &commonpb.Payload{}
		if err := proto.Unmarshal(payloadBytes, result[i]); err != nil {
			return nil, fmt.Errorf("unmarshal Nexus payload: %w", err)
		}
	}
	return result, nil
}

func intTestNexusCallerWorkflow(ctx workflow.Context, hmacEndpointName, zlibEndpointName string) (string, error) {
	// Schedule both operations before awaiting either result. Each future must
	// retain the converter selected for its own Nexus operation context.
	hmacFuture := workflow.NewNexusClient(hmacEndpointName, intTestNexusService).ExecuteOperation(
		ctx,
		intTestNexusOperation,
		intTestNexusInput{Value: "hmac"},
		workflow.NexusOperationOptions{},
	)
	zlibFuture := workflow.NewNexusClient(zlibEndpointName, intTestNexusService).ExecuteOperation(
		ctx,
		intTestNexusOperation.Name(),
		intTestNexusInput{Value: "zlib"},
		workflow.NexusOperationOptions{},
	)

	var results []string
	var operationErr error
	selector := workflow.NewSelector(ctx)
	handleFuture := func(f workflow.Future) {
		var result string
		if err := f.Get(ctx, &result); err != nil {
			operationErr = err
			return
		}
		results = append(results, result)
	}
	selector.AddFuture(hmacFuture, handleFuture)
	selector.AddFuture(zlibFuture, handleFuture)
	for len(results) < 2 && operationErr == nil {
		selector.Select(ctx)
	}
	return strings.Join(results, "|"), operationErr
}

func (ts *IntegrationTestSuite) TestSerializationContext_NexusCallerEndpointIsolation() {
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "test creates Nexus endpoints through Operator Service")

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	hmacEndpointName := "nexus-ser-ctx-hmac-" + uuid.NewString()
	zlibEndpointName := "nexus-ser-ctx-zlib-" + uuid.NewString()
	hmacCodec := &intTestNexusHMACCodec{key: []byte("nexus-hmac-key")}
	zlibCodec := converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})
	codecSelector := &intTestNexusCodecSelector{codecs: map[string]converter.PayloadCodec{
		intTestNexusContextKey(hmacEndpointName, intTestNexusService, intTestNexusOperation.Name()): hmacCodec,
		intTestNexusContextKey(zlibEndpointName, intTestNexusService, intTestNexusOperation.Name()): zlibCodec,
	}}

	callerDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codecSelector)
	callerClient, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = callerDC
	})
	ts.NoError(err)
	defer callerClient.Close()

	hmacHandlerTaskQueue := "nexus-ser-ctx-handler-hmac-" + uuid.NewString()
	zlibHandlerTaskQueue := "nexus-ser-ctx-handler-zlib-" + uuid.NewString()
	hmacEndpointResponse, err := callerClient.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: hmacEndpointName,
			Target: &nexuspb.EndpointTarget{Variant: &nexuspb.EndpointTarget_Worker_{
				Worker: &nexuspb.EndpointTarget_Worker{
					Namespace: ts.config.Namespace,
					TaskQueue: hmacHandlerTaskQueue,
				},
			}},
		},
	})
	ts.NoError(err)
	defer func() {
		_, _ = callerClient.OperatorService().DeleteNexusEndpoint(ctx, &operatorservice.DeleteNexusEndpointRequest{
			Id: hmacEndpointResponse.Endpoint.Id, Version: hmacEndpointResponse.Endpoint.Version,
		})
	}()
	zlibEndpointResponse, err := callerClient.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: zlibEndpointName,
			Target: &nexuspb.EndpointTarget{Variant: &nexuspb.EndpointTarget_Worker_{
				Worker: &nexuspb.EndpointTarget_Worker{
					Namespace: ts.config.Namespace,
					TaskQueue: zlibHandlerTaskQueue,
				},
			}},
		},
	})
	ts.NoError(err)
	defer func() {
		_, _ = callerClient.OperatorService().DeleteNexusEndpoint(ctx, &operatorservice.DeleteNexusEndpointRequest{
			Id: zlibEndpointResponse.Endpoint.Id, Version: zlibEndpointResponse.Endpoint.Version,
		})
	}()

	// Each handler uses the codec selected for its endpoint. Inputs and results
	// round trip only if the caller selects and retains that same codec.
	hmacHandlerDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), hmacCodec)
	hmacHandlerClient, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = hmacHandlerDC
	})
	ts.NoError(err)
	defer hmacHandlerClient.Close()
	hmacHandlerWorker := worker.New(hmacHandlerClient, hmacHandlerTaskQueue, worker.Options{DisableWorkflowWorker: true})
	hmacService := nexus.NewService(intTestNexusService)
	ts.NoError(hmacService.Register(intTestNexusOperation))
	hmacHandlerWorker.RegisterNexusService(hmacService)
	ts.NoError(hmacHandlerWorker.Start())
	defer hmacHandlerWorker.Stop()

	zlibHandlerDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), zlibCodec)
	zlibHandlerClient, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = zlibHandlerDC
	})
	ts.NoError(err)
	defer zlibHandlerClient.Close()
	zlibHandlerWorker := worker.New(zlibHandlerClient, zlibHandlerTaskQueue, worker.Options{DisableWorkflowWorker: true})
	zlibService := nexus.NewService(intTestNexusService)
	ts.NoError(zlibService.Register(intTestNexusOperation))
	zlibHandlerWorker.RegisterNexusService(zlibService)
	ts.NoError(zlibHandlerWorker.Start())
	defer zlibHandlerWorker.Stop()

	callerTaskQueue := "nexus-ser-ctx-caller-" + uuid.NewString()
	callerWorker := worker.New(callerClient, callerTaskQueue, worker.Options{})
	callerWorker.RegisterWorkflow(intTestNexusCallerWorkflow)
	ts.NoError(callerWorker.Start())
	defer callerWorker.Stop()

	run, err := callerClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "nexus-ser-ctx-caller-" + uuid.NewString(),
		TaskQueue: callerTaskQueue,
	}, intTestNexusCallerWorkflow, hmacEndpointName, zlibEndpointName)
	ts.NoError(err)
	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.ElementsMatch([]string{"hmac", "zlib"}, strings.Split(result, "|"))
}
