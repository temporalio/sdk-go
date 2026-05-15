package test_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// nexusEndpointEnvelope is the JSON envelope written into Payload.Data by intTestNexusEndpointCodec.
// The stamp and original payload bytes survive the Temporal server round-trip because they are
// stored in the Data field (not metadata, which the server may strip).
type nexusEndpointEnvelope struct {
	Stamp        string `json:"stamp"`
	OrigEncoding string `json:"origEncoding"`
	OrigData     []byte `json:"origData"`
}

const nexusEndpointCodecEncoding = "test/nexus-endpoint-stamped"

// intTestNexusEndpointCodec stamps payloads with the Nexus operation's Endpoint name on Encode,
// and verifies the stamp matches on Decode. Used to prove NexusSerializationContext flows
// through to the codec at every wired site.
//
// The stamp is embedded in the payload Data (not metadata) so it survives the Temporal server
// round-trip: the server may strip unknown metadata keys from payloads it relays.
type intTestNexusEndpointCodec struct {
	endpoint string // empty when no Nexus context is set (pass-through)
}

func (c *intTestNexusEndpointCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	if nsc, ok := ctx.(converter.NexusSerializationContext); ok {
		return &intTestNexusEndpointCodec{endpoint: nsc.Endpoint}
	}
	return c // pass-through for Workflow/Activity/other contexts
}

func (c *intTestNexusEndpointCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		env := nexusEndpointEnvelope{
			Stamp:        c.endpoint,
			OrigEncoding: string(p.Metadata["encoding"]),
			OrigData:     p.Data,
		}
		wrapped, err := json.Marshal(env)
		if err != nil {
			return nil, fmt.Errorf("intTestNexusEndpointCodec.Encode: %w", err)
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				"encoding": []byte(nexusEndpointCodecEncoding),
			},
			Data: wrapped,
		}
	}
	return result, nil
}

func (c *intTestNexusEndpointCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		enc := string(p.Metadata["encoding"])
		// Pass through payloads that were not encoded by this codec.
		if enc != nexusEndpointCodecEncoding {
			result[i] = proto.Clone(p).(*commonpb.Payload)
			continue
		}
		var env nexusEndpointEnvelope
		if err := json.Unmarshal(p.Data, &env); err != nil {
			return nil, fmt.Errorf("intTestNexusEndpointCodec.Decode: unmarshal envelope: %w", err)
		}
		// When endpoint is non-empty (Nexus context), verify the stamp matches.
		// When endpoint is empty (pass-through: WorkflowSerializationContext etc.), accept any stamp.
		if c.endpoint != "" && env.Stamp != c.endpoint {
			return nil, fmt.Errorf("nexus-endpoint-stamp mismatch: got %q, want %q", env.Stamp, c.endpoint)
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				"encoding": []byte(env.OrigEncoding),
			},
			Data: env.OrigData,
		}
	}
	return result, nil
}

// newNexusSerCtxClientAndWorker creates a client and worker both using the endpoint-keyed codec CDC.
func newNexusSerCtxClientAndWorker(t *testing.T, tc *testContext) (client.Client, worker.Worker) {
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &intTestNexusEndpointCodec{})

	c, err := client.Dial(client.Options{
		HostPort:          tc.testConfig.ServiceAddr,
		Namespace:         tc.testConfig.Namespace,
		Logger:            ilog.NewDefaultLogger(),
		DataConverter:     codecDC,
		ConnectionOptions: client.ConnectionOptions{TLS: tc.testConfig.TLS},
	})
	require.NoError(t, err)

	w := worker.New(c, tc.taskQueue, worker.Options{})
	return c, w
}

// nexusSerCtxEchoOp is a sync Nexus operation that echoes its string input.
var nexusSerCtxEchoOp = nexus.NewSyncOperation(
	"nexus-ser-ctx-echo-op",
	func(_ context.Context, s string, _ nexus.StartOperationOptions) (string, error) {
		return s, nil
	},
)

// nexusSerCtxCallerWorkflow calls nexusSerCtxEchoOp via the Nexus client and returns the result.
func nexusSerCtxCallerWorkflow(ctx workflow.Context, endpoint string, input string) (string, error) {
	c := workflow.NewNexusClient(endpoint, "nexus-ser-ctx-service")
	fut := c.ExecuteOperation(ctx, nexusSerCtxEchoOp, input, workflow.NexusOperationOptions{})
	var result string
	if err := fut.Get(ctx, &result); err != nil {
		return "", err
	}
	return result, nil
}

func TestNexusSerializationContext_SyncOperation_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()

	tc := newTestContext(t, ctx)

	c, w := newNexusSerCtxClientAndWorker(t, tc)
	defer c.Close()

	service := nexus.NewService("nexus-ser-ctx-service")
	require.NoError(t, service.Register(nexusSerCtxEchoOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(nexusSerCtxCallerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:           tc.taskQueue,
		WorkflowTaskTimeout: time.Second,
	}, nexusSerCtxCallerWorkflow, tc.endpoint, "hello")
	require.NoError(t, err)

	var result string
	require.NoError(t, run.Get(ctx, &result))
	require.Equal(t, "hello", result)
}

// nexusSerCtxHandlerWorkflow is a simple workflow used as a workflow-run Nexus operation handler.
func nexusSerCtxHandlerWorkflow(_ workflow.Context, input string) (string, error) {
	return input, nil
}

// nexusSerCtxWfRunCallerWorkflow calls the workflow-run Nexus op and returns the result.
func nexusSerCtxWfRunCallerWorkflow(ctx workflow.Context, endpoint string, input string) (string, error) {
	op := temporalnexus.NewWorkflowRunOperation(
		"nexus-ser-ctx-wfrun-op",
		nexusSerCtxHandlerWorkflow,
		func(_ context.Context, s string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{ID: soo.RequestID}, nil
		},
	)
	c := workflow.NewNexusClient(endpoint, "nexus-ser-ctx-wfrun-service")
	fut := c.ExecuteOperation(ctx, op, input, workflow.NexusOperationOptions{})
	var result string
	if err := fut.Get(ctx, &result); err != nil {
		return "", err
	}
	return result, nil
}

func TestNexusSerializationContext_WorkflowRunOperation_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()

	tc := newTestContext(t, ctx)

	c, w := newNexusSerCtxClientAndWorker(t, tc)
	defer c.Close()

	op := temporalnexus.NewWorkflowRunOperation(
		"nexus-ser-ctx-wfrun-op",
		nexusSerCtxHandlerWorkflow,
		func(_ context.Context, s string, soo nexus.StartOperationOptions) (client.StartWorkflowOptions, error) {
			return client.StartWorkflowOptions{ID: soo.RequestID}, nil
		},
	)

	service := nexus.NewService("nexus-ser-ctx-wfrun-service")
	require.NoError(t, service.Register(op))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(nexusSerCtxHandlerWorkflow)
	w.RegisterWorkflow(nexusSerCtxWfRunCallerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:           tc.taskQueue,
		WorkflowTaskTimeout: time.Second,
	}, nexusSerCtxWfRunCallerWorkflow, tc.endpoint, "world")
	require.NoError(t, err)

	var result string
	require.NoError(t, run.Get(ctx, &result))
	require.Equal(t, "world", result)
}

func TestNexusSerializationContext_VanillaConverter_NoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNexusTestTimeout)
	defer cancel()

	tc := newTestContext(t, ctx)

	// Use tc.client directly — no custom codec, just the default data converter.
	w := worker.New(tc.client, tc.taskQueue, worker.Options{})

	service := nexus.NewService("nexus-ser-ctx-service")
	require.NoError(t, service.Register(nexusSerCtxEchoOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(nexusSerCtxCallerWorkflow)
	require.NoError(t, w.Start())
	t.Cleanup(w.Stop)

	run, err := tc.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:           tc.taskQueue,
		WorkflowTaskTimeout: time.Second,
	}, nexusSerCtxCallerWorkflow, tc.endpoint, "vanilla")
	require.NoError(t, err)

	var result string
	require.NoError(t, run.Get(ctx, &result))
	require.Equal(t, "vanilla", result)
}
