package workflowstreams

import (
	"context"
	"errors"
	"iter"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
)

// Options configures a Client.
type Options struct {
	// BatchInterval is the interval between automatic flushes. Default: 2s.
	BatchInterval time.Duration
	// MaxBatchSize triggers a flush once the buffer reaches this many items.
	// Zero disables size-based flushing.
	MaxBatchSize int
	// MaxRetryDuration is the maximum time to retry a failed flush before
	// returning a FlushTimeoutError. Must be less than the workflow's publisher
	// TTL (default 15m) to preserve exactly-once delivery. Default: 10m.
	MaxRetryDuration time.Duration
	// DataConverter is used to turn published values into Payloads and is the
	// converter callers should use to decode subscribed items. Default:
	// converter.GetDefaultDataConverter().
	DataConverter converter.DataConverter
}

// SubscribeOptions configures a subscription.
type SubscribeOptions struct {
	// Topics filters the subscription. Empty or nil means all topics.
	Topics []string
	// FromOffset is the global offset to start from. Zero means the beginning.
	FromOffset int64
	// PollCooldown is the minimum interval between polls when no more items are
	// immediately ready. Default: 100ms.
	PollCooldown time.Duration
}

// Client publishes to and subscribes from a workflow stream from external code
// (activities, starters, other workflows). The publish path is owned by an
// internal publisher; the Client itself holds the target workflow and the read
// (subscribe/query) surface.
type Client struct {
	c          client.Client
	workflowID string
	followCAN  bool
	pub        *publisher

	mu           sync.Mutex
	topicHandles map[string]*TopicHandle
}

// NewClient creates a Client targeting workflowID through the given Temporal
// client. The returned Client follows continue-as-new chains in Subscribe.
func NewClient(c client.Client, workflowID string, opts Options) *Client {
	dc := opts.DataConverter
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}
	wsc := &Client{
		c:            c,
		workflowID:   workflowID,
		followCAN:    true,
		topicHandles: map[string]*TopicHandle{},
	}
	wsc.pub = newPublisher(func(ctx context.Context, in PublishInput) error {
		return c.SignalWorkflow(ctx, workflowID, "", PublishSignalName, in)
	}, dc, opts)
	return wsc
}

// NewClientFromActivity creates a Client targeting the current activity's parent
// workflow, using the activity's Temporal client. It returns an error if the
// activity has no parent workflow (a standalone activity); in that case use
// NewClient with an explicit workflow ID.
func NewClientFromActivity(ctx context.Context, opts Options) (*Client, error) {
	info := activity.GetInfo(ctx)
	if info.WorkflowExecution.ID == "" {
		return nil, errors.New("workflowstreams: NewClientFromActivity requires an activity scheduled by a workflow; " +
			"from a standalone activity use NewClient with an explicit workflow ID")
	}
	return NewClient(activity.GetClient(ctx), info.WorkflowExecution.ID, opts), nil
}

// Topic returns a handle for publishing to and subscribing from name. Repeated
// calls with the same name return the same handle.
func (c *Client) Topic(name string) *TopicHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.topicHandles[name]; ok {
		return h
	}
	h := &TopicHandle{name: name, client: c}
	c.topicHandles[name] = h
	return h
}

// Flush sends buffered (and pending) items and waits for server confirmation.
// It returns once the items buffered at call time have been signaled to the
// workflow and acknowledged. Returns a FlushTimeoutError if a pending batch
// cannot be sent within MaxRetryDuration.
func (c *Client) Flush(ctx context.Context) error { return c.pub.flush(ctx) }

// Close stops the background publisher and drains any remaining items. Call it
// (e.g. via defer) to guarantee a final flush. It surfaces any deferred
// FlushTimeoutError from a prior background flush failure.
func (c *Client) Close(ctx context.Context) error { return c.pub.close(ctx) }

// GetOffset queries the current global offset of the stream.
func (c *Client) GetOffset(ctx context.Context) (int64, error) {
	val, err := c.c.QueryWorkflow(ctx, c.workflowID, "", OffsetQueryName)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := val.Get(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Subscribe returns a range-over-func iterator that long-polls for new items.
// Iterate with:
//
//	for item, err := range c.Subscribe(ctx, opts) {
//		if err != nil { ... }
//		// use item
//	}
//
// Breaking out of the loop or cancelling ctx stops the subscription and tears
// down the poll loop. Each yielded item carries the raw *commonpb.Payload in
// Data; decode it with your data converter. The iterator ends cleanly when the
// workflow reaches a terminal state, and automatically follows continue-as-new
// chains.
func (c *Client) Subscribe(ctx context.Context, opts SubscribeOptions) iter.Seq2[WorkflowStreamItem, error] {
	return func(yield func(WorkflowStreamItem, error) bool) {
		pollCooldown := opts.PollCooldown
		if pollCooldown <= 0 {
			pollCooldown = defaultPollCooldown
		}
		topics := opts.Topics
		if topics == nil {
			topics = []string{}
		}
		offset := opts.FromOffset

		for {
			if err := ctx.Err(); err != nil {
				yield(WorkflowStreamItem{}, err)
				return
			}

			var result PollResult
			handle, err := c.c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
				WorkflowID:   c.workflowID,
				UpdateName:   PollUpdateName,
				Args:         []any{PollInput{Topics: topics, FromOffset: offset}},
				WaitForStage: client.WorkflowUpdateStageCompleted,
			})
			if err == nil {
				err = handle.Get(ctx, &result)
			}
			if err != nil {
				var appErr *temporal.ApplicationError
				if errors.As(err, &appErr) && appErr.Type() == ErrTypeTruncatedOffset {
					// Fell behind truncation; restart from the beginning of
					// whatever still exists.
					offset = 0
					continue
				}
				// The workflow may have continued-as-new or completed between
				// polls. Follow the chain, exit cleanly on a terminal state,
				// otherwise surface the error.
				if followed := c.followContinueAsNew(ctx); followed {
					continue
				}
				if c.isTerminal(ctx) {
					return
				}
				yield(WorkflowStreamItem{}, err)
				return
			}

			for _, wi := range result.Items {
				payload, derr := decodePayloadWire(wi.Data)
				if derr != nil {
					yield(WorkflowStreamItem{}, derr)
					return
				}
				if !yield(WorkflowStreamItem{Topic: wi.Topic, Data: payload, Offset: wi.Offset}, nil) {
					return
				}
			}
			offset = result.NextOffset

			if !result.MoreReady {
				select {
				case <-time.After(pollCooldown):
				case <-ctx.Done():
					yield(WorkflowStreamItem{}, ctx.Err())
					return
				}
			}
		}
	}
}

// followContinueAsNew reports whether the target workflow continued-as-new.
// Because polls use an empty run ID they always address the latest run, so the
// caller only needs to retry.
func (c *Client) followContinueAsNew(ctx context.Context) bool {
	if !c.followCAN {
		return false
	}
	desc, err := c.c.DescribeWorkflowExecution(ctx, c.workflowID, "")
	if err != nil {
		return false
	}
	return desc.GetWorkflowExecutionInfo().GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW
}

func (c *Client) isTerminal(ctx context.Context) bool {
	desc, err := c.c.DescribeWorkflowExecution(ctx, c.workflowID, "")
	if err != nil {
		return false
	}
	switch desc.GetWorkflowExecutionInfo().GetStatus() {
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
		enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return true
	}
	return false
}
