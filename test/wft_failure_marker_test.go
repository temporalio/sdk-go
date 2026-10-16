package test_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// These integration tests exercise converter.WorkflowTaskFailureError end to end
// against a live server: a codec returns the marker once while decoding a
// workflow-side payload, the current Workflow Task fails and is retried by the
// server, and the retry decodes successfully so the Workflow Execution completes
// without ever failing. They require a running Temporal server (like the rest of
// the test/ integration suite).

// failOnceDecodeCodec returns a WorkflowTaskFailureError the first time it
// decodes a payload whose bytes contain the given sentinel, and passes through
// on every subsequent call. Keying the one-shot failure on a sentinel keeps it
// deterministic regardless of the order in which the client and worker (which
// share this codec instance) invoke Decode. Encode is a passthrough.
type failOnceDecodeCodec struct {
	sentinel    string
	mu          sync.Mutex
	failed      bool
	decodeCalls int32
}

func (c *failOnceDecodeCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}

func (c *failOnceDecodeCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	atomic.AddInt32(&c.decodeCalls, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.failed {
		for _, p := range payloads {
			if bytes.Contains(p.Data, []byte(c.sentinel)) {
				c.failed = true
				return nil, converter.NewWorkflowTaskFailureError(
					fmt.Errorf("transient codec decode blip on %q", c.sentinel))
			}
		}
	}
	return payloads, nil
}

func wftMarkerInputWorkflow(_ workflow.Context, input string) (string, error) {
	return "done:" + input, nil
}

func (ts *IntegrationTestSuite) newFailOnceClientAndWorker(taskQueue string, codec *failOnceDecodeCodec) (client.Client, worker.Worker) {
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	c, err := client.Dial(client.Options{
		HostPort:          ts.config.ServiceAddr,
		Namespace:         ts.config.Namespace,
		Logger:            ilog.NewDefaultLogger(),
		DataConverter:     codecDC,
		ConnectionOptions: client.ConnectionOptions{TLS: ts.config.TLS},
	})
	ts.NoError(err)
	// The worker inherits the client's DataConverter, so client and worker share
	// this one codec instance (and its one-shot failure state).
	w := worker.New(c, taskQueue, worker.Options{})
	return c, w
}

// A codec that returns WorkflowTaskFailureError while decoding workflow input
// fails only the current Workflow Task; the server retries it, the retry decodes
// cleanly, and the Workflow Execution completes.
func (ts *IntegrationTestSuite) TestWorkflowTaskFailureMarker_InputDecode_RetriesThenCompletes() {
	sentinel := "wft-marker-input-sentinel"
	codec := &failOnceDecodeCodec{sentinel: sentinel}
	taskQueue := "test-wft-marker-input-" + ts.T().Name()
	c, w := ts.newFailOnceClientAndWorker(taskQueue, codec)
	defer c.Close()

	w.RegisterWorkflow(wftMarkerInputWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "wft-marker-input-" + ts.T().Name(),
		TaskQueue: taskQueue,
	}, wftMarkerInputWorkflow, sentinel)
	ts.NoError(err)

	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.Equal("done:"+sentinel, result)
	// The codec failed input decode once and succeeded on the retry, so Decode
	// ran on at least two Workflow Task attempts.
	ts.True(codec.failed)
	ts.GreaterOrEqual(atomic.LoadInt32(&codec.decodeCalls), int32(2))
}

var wftMarkerActivityRuns int32

func wftMarkerActivity(_ context.Context) (string, error) {
	atomic.AddInt32(&wftMarkerActivityRuns, 1)
	return "wft-marker-activity-sentinel", nil
}

func wftMarkerActivityWorkflow(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var result string
	if err := workflow.ExecuteActivity(ctx, wftMarkerActivity).Get(ctx, &result); err != nil {
		return "", err
	}
	return "done:" + result, nil
}

// A codec that returns WorkflowTaskFailureError while decoding an activity
// result (delivered to the workflow via Future.Get) fails only the current
// Workflow Task. The retry replays the already-completed activity from history
// (so the activity runs exactly once) and decodes cleanly, and the Workflow
// Execution completes.
func (ts *IntegrationTestSuite) TestWorkflowTaskFailureMarker_ActivityResultDecode_RetriesThenCompletes() {
	atomic.StoreInt32(&wftMarkerActivityRuns, 0)
	sentinel := "wft-marker-activity-sentinel"
	codec := &failOnceDecodeCodec{sentinel: sentinel}
	taskQueue := "test-wft-marker-activity-" + ts.T().Name()
	c, w := ts.newFailOnceClientAndWorker(taskQueue, codec)
	defer c.Close()

	w.RegisterWorkflow(wftMarkerActivityWorkflow)
	w.RegisterActivity(wftMarkerActivity)
	ts.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        "wft-marker-activity-" + ts.T().Name(),
		TaskQueue: taskQueue,
	}, wftMarkerActivityWorkflow)
	ts.NoError(err)

	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.Equal("done:"+sentinel, result)
	// The activity ran exactly once: the WFT retry replayed its cached result
	// from history rather than re-executing it.
	ts.Equal(int32(1), atomic.LoadInt32(&wftMarkerActivityRuns))
	ts.True(codec.failed)
}
