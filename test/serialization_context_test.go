package test_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
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
	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
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

	c, err := client.Dial(client.Options{
		HostPort:          ts.config.ServiceAddr,
		Namespace:         ts.config.Namespace,
		Logger:            ilog.NewDefaultLogger(),
		DataConverter:     codecDC,
		ConnectionOptions: client.ConnectionOptions{TLS: ts.config.TLS},
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
