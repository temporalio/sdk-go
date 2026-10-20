package test_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/workflowstreams"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type workflowStreamsInput struct {
	Empty    bool
	Mode     string
	Size     int
	Continue bool
	Tail     bool
	State    *workflowstreams.WorkflowStreamState
}

func workflowStreamsWorkflow(ctx workflow.Context, input workflowStreamsInput) error {
	stream, err := workflowstreams.NewWorkflowStream(ctx, input.State)
	if err != nil {
		return err
	}
	if input.State == nil && !input.Empty {
		for _, topic := range []string{"events", "other", "events", "events"} {
			if err := stream.Topic(topic).Publish([]byte(strings.Repeat("x", input.Size))); err != nil {
				return err
			}
		}
		if err := stream.Truncate(1); err != nil {
			return err
		}
	}
	if input.Mode == "immediate" {
		return nil
	}
	finish := workflow.GetSignalChannel(ctx, "finish")
	if err := workflow.Await(ctx, func() bool { return finish.ReceiveAsync(nil) }); err != nil {
		return err
	}
	if input.Tail {
		if err := stream.Topic("events").Publish([]byte("tail")); err != nil {
			return err
		}
	}
	if input.Continue {
		return stream.NewContinueAsNewError(ctx, workflowStreamsWorkflow, func(state *workflowstreams.WorkflowStreamState) []any {
			return []any{workflowStreamsInput{Mode: "immediate", State: state}}
		})
	}
	if input.Mode == "failed" {
		return errors.New("requested workflow failure")
	}
	return nil
}

func (ts *IntegrationTestSuite) startWorkflowStreamsWorker() {
	if !ts.workerStopped {
		ts.worker.Stop()
	}
	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.worker.RegisterWorkflow(workflowStreamsWorkflow)
	ts.worker.RegisterWorkflow(SimplestWorkflow)
	ts.NoError(ts.worker.Start())
	ts.workerStopped = false
}

func workflowStreamsHistory(ctx context.Context, c client.Client, run client.WorkflowRun) (*historypb.History, error) {
	var history historypb.History
	iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return nil, err
		}
		history.Events = append(history.Events, event)
	}
	return &history, nil
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsClosedRuns() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), time.Minute)
	defer cancel()
	for _, mode := range []string{"completed", "failed", "canceled", "terminated", "timed-out"} {
		ts.Run(mode, func() {
			r := require.New(ts.T())
			ts.startWorkflowStreamsWorker()
			options := client.StartWorkflowOptions{ID: "closed-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName}
			if mode == "timed-out" {
				options.WorkflowRunTimeout = 3 * time.Second
			}
			run, err := ts.client.ExecuteWorkflow(ctx, options, workflowStreamsWorkflow, workflowStreamsInput{Mode: mode, Size: 400_000})
			r.NoError(err)
			stream := workflowstreams.NewClient(ts.client, run.GetID(), workflowstreams.Options{})
			defer func() { r.NoError(stream.Close(ctx)) }()
			offset, err := stream.GetOffset(ctx)
			r.NoError(err)
			r.EqualValues(4, offset)
			switch mode {
			case "canceled":
				r.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))
			case "terminated":
				r.NoError(ts.client.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test"))
			case "timed-out":
			default:
				r.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
			}
			err = run.Get(ctx, nil)
			if mode == "completed" {
				r.NoError(err)
			} else {
				r.Error(err)
			}
			history, err := workflowStreamsHistory(ctx, ts.client, run)
			r.NoError(err)
			replayer := worker.NewWorkflowReplayer()
			replayer.RegisterWorkflow(workflowStreamsWorkflow)
			r.NoError(replayer.ReplayWorkflowHistory(nil, history))

			// Replacing the worker forces every closed read to reconstruct retained
			// stream state from history instead of relying on the live cache.
			ts.startWorkflowStreamsWorker()
			for _, test := range []struct {
				options workflowstreams.SubscribeOptions
				offsets []int64
			}{
				{workflowstreams.SubscribeOptions{Topics: []string{"events"}}, []int64{2, 3}},
				{workflowstreams.SubscribeOptions{Topics: []string{"events"}, FromOffset: -1}, []int64{2, 3}},
				{workflowstreams.SubscribeOptions{Topics: []string{"events"}, FromOffset: 3}, []int64{3}},
				{workflowstreams.SubscribeOptions{FromOffset: 4}, nil},
				{workflowstreams.SubscribeOptions{Topics: []string{"missing"}}, nil},
			} {
				var offsets []int64
				for item, err := range stream.Subscribe(ctx, test.options) {
					r.NoError(err)
					offsets = append(offsets, item.Offset)
					var data []byte
					r.NoError(converter.GetDefaultDataConverter().FromPayload(item.Data, &data))
					r.Len(data, 400_000)
				}
				r.Equal(test.offsets, offsets)
			}
			after, err := workflowStreamsHistory(ctx, ts.client, run)
			r.NoError(err)
			r.Len(after.Events, len(history.Events), "closed reads must add no history events")
		})
	}
}

type workflowStreamsAcceptedClient struct {
	client.Client
	accepted chan struct{}
	once     sync.Once
}

func (c *workflowStreamsAcceptedClient) UpdateWorkflow(ctx context.Context, options client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	handle, err := c.Client.UpdateWorkflow(ctx, options)
	if err == nil {
		c.once.Do(func() { close(c.accepted) })
	}
	return handle, err
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsClosureDuringPoll() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	ts.startWorkflowStreamsWorker()
	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "closing-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName}, workflowStreamsWorkflow, workflowStreamsInput{Mode: "completed", Size: 1, Tail: true})
	ts.NoError(err)
	accepted := &workflowStreamsAcceptedClient{Client: ts.client, accepted: make(chan struct{})}
	stream := workflowstreams.NewClient(accepted, run.GetID(), workflowstreams.Options{})
	defer func() { ts.NoError(stream.Close(ctx)) }()
	result := make(chan []int64, 1)
	errors := make(chan error, 1)
	go func() {
		var offsets []int64
		for item, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{FromOffset: 4}) {
			if err != nil {
				errors <- err
				return
			}
			offsets = append(offsets, item.Offset)
		}
		result <- offsets
	}()
	select {
	case <-accepted.accepted:
	case <-ctx.Done():
		ts.FailNow("poll was not accepted", "%v", ctx.Err())
	}
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
	ts.NoError(run.Get(ctx, nil))
	select {
	case offsets := <-result:
		ts.Equal([]int64{4}, offsets)
	case err := <-errors:
		ts.NoError(err)
	case <-ctx.Done():
		ts.FailNow("subscription did not drain", "%v", ctx.Err())
	}
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsContinueAsNewThenClose() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	ts.startWorkflowStreamsWorker()
	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "continued-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName}, workflowStreamsWorkflow, workflowStreamsInput{Mode: "completed", Size: 1, Continue: true})
	ts.NoError(err)
	stream := workflowstreams.NewClient(ts.client, run.GetID(), workflowstreams.Options{})
	defer func() { ts.NoError(stream.Close(ctx)) }()
	_, err = stream.GetOffset(ctx)
	ts.NoError(err)
	ts.NoError(ts.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
	ts.NoError(run.Get(ctx, nil))
	var offsets []int64
	for item, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{Topics: []string{"events"}}) {
		ts.NoError(err)
		offsets = append(offsets, item.Offset)
	}
	ts.Equal([]int64{2, 3}, offsets)
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsEmptyAndMissingQuery() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	ts.startWorkflowStreamsWorker()
	for _, missing := range []bool{false, true} {
		var wf any = workflowStreamsWorkflow
		args := []any{workflowStreamsInput{Mode: "immediate", Empty: true}}
		if missing {
			wf = SimplestWorkflow
			args = nil
		}
		run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "empty-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName}, wf, args...)
		ts.NoError(err)
		ts.NoError(run.Get(ctx, nil))
		stream := workflowstreams.NewClient(ts.client, run.GetID(), workflowstreams.Options{})
		var got error
		for _, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{}) {
			got = err
		}
		ts.NoError(stream.Close(ctx))
		if missing {
			ts.ErrorContains(got, workflowstreams.PollQueryName)
		} else {
			ts.NoError(got)
		}
	}
}

type workflowStreamsRejectInterceptor struct {
	interceptor.ClientInterceptorBase
}

func (*workflowStreamsRejectInterceptor) InterceptClient(next interceptor.ClientOutboundInterceptor) interceptor.ClientOutboundInterceptor {
	return &workflowStreamsRejectOutbound{ClientOutboundInterceptorBase: interceptor.ClientOutboundInterceptorBase{Next: next}}
}

type workflowStreamsRejectOutbound struct {
	interceptor.ClientOutboundInterceptorBase
}

func (i *workflowStreamsRejectOutbound) QueryWorkflow(ctx context.Context, input *interceptor.ClientQueryWorkflowInput) (converter.EncodedValue, error) {
	input.QueryRejectCondition = enumspb.QUERY_REJECT_CONDITION_NOT_OPEN
	return i.Next.QueryWorkflow(ctx, input)
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsQueryRejection() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	ts.startWorkflowStreamsWorker()
	run, err := ts.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "rejected-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName}, workflowStreamsWorkflow, workflowStreamsInput{Mode: "immediate", Size: 1})
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.Interceptors = []interceptor.ClientInterceptor{&workflowStreamsRejectInterceptor{}}
	})
	ts.NoError(err)
	defer c.Close()
	stream := workflowstreams.NewClient(c, run.GetID(), workflowstreams.Options{})
	defer func() { ts.NoError(stream.Close(ctx)) }()
	var got error
	for _, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{}) {
		got = err
	}
	var rejected *temporal.QueryRejectedError
	ts.ErrorAs(got, &rejected)
}

type workflowStreamsReuseClient struct {
	client.Client
	beforeSecondQuery func(context.Context) error
	queryRuns         []string
}

func (c *workflowStreamsReuseClient) QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...any) (converter.EncodedValue, error) {
	c.queryRuns = append(c.queryRuns, runID)
	if len(c.queryRuns) == 2 {
		if err := c.beforeSecondQuery(ctx); err != nil {
			return nil, err
		}
	}
	return c.Client.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsReuseAndConcurrentSubscribers() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	ts.startWorkflowStreamsWorker()
	options := client.StartWorkflowOptions{ID: "reused-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName}
	run, err := ts.client.ExecuteWorkflow(ctx, options, workflowStreamsWorkflow, workflowStreamsInput{Mode: "immediate", Size: 400_000})
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
	c := &workflowStreamsReuseClient{Client: ts.client, beforeSecondQuery: func(ctx context.Context) error {
		next, err := ts.client.ExecuteWorkflow(ctx, options, workflowStreamsWorkflow, workflowStreamsInput{Mode: "immediate", Size: 1})
		if err != nil {
			return err
		}
		return next.Get(ctx, nil)
	}}
	stream := workflowstreams.NewClient(c, run.GetID(), workflowstreams.Options{})
	var offsets []int64
	for item, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{Topics: []string{"events"}}) {
		ts.NoError(err)
		offsets = append(offsets, item.Offset)
		var data []byte
		ts.NoError(converter.GetDefaultDataConverter().FromPayload(item.Data, &data))
		ts.Len(data, 400_000)
	}
	ts.Equal([]int64{2, 3}, offsets)
	ts.Equal([]string{run.GetRunID(), run.GetRunID()}, c.queryRuns)
	ts.NoError(stream.Close(ctx))

	stream = workflowstreams.NewClient(ts.client, run.GetID(), workflowstreams.Options{})
	defer func() { ts.NoError(stream.Close(ctx)) }()
	type result struct {
		offsets []int64
		err     error
	}
	results := make(chan result, 4)
	for range 4 {
		go func() {
			var res result
			for item, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{Topics: []string{"events"}}) {
				if err != nil {
					res.err = err
					break
				}
				res.offsets = append(res.offsets, item.Offset)
			}
			results <- res
		}()
	}
	for range 4 {
		select {
		case res := <-results:
			ts.NoError(res.err)
			ts.Equal([]int64{2, 3}, res.offsets)
		case <-ctx.Done():
			ts.FailNow("concurrent subscriptions did not finish", "%v", ctx.Err())
		}
	}
}

func (ts *IntegrationTestSuite) TestWorkflowStreamsCodecEnvelope() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	})
	ts.NoError(err)
	defer c.Close()
	w := worker.New(c, ts.taskQueueName+"-codec", worker.Options{})
	w.RegisterWorkflow(workflowStreamsWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "codec-stream-" + uuid.NewString(), TaskQueue: ts.taskQueueName + "-codec"}, workflowStreamsWorkflow, workflowStreamsInput{Mode: "immediate", Size: 1})
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
	stream := workflowstreams.NewClient(c, run.GetID(), workflowstreams.Options{})
	defer func() { ts.NoError(stream.Close(ctx)) }()
	var offsets []int64
	for item, err := range stream.Subscribe(ctx, workflowstreams.SubscribeOptions{Topics: []string{"events"}}) {
		ts.NoError(err)
		offsets = append(offsets, item.Offset)
		var data []byte
		ts.NoError(converter.GetDefaultDataConverter().FromPayload(item.Data, &data))
		ts.Equal([]byte("x"), data)
	}
	ts.Equal([]int64{2, 3}, offsets)
}
