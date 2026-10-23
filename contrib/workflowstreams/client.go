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
	// PayloadConverters customize how published values are serialized into the
	// per-item Payloads carried inside each batch. They are combined into a
	// CompositeDataConverter in the order given (as with
	// converter.NewCompositeDataConverter), so the last one should be a
	// catch-all such as converter.NewJSONPayloadConverter.
	//
	// Only payload conversion happens here — never a payload codec
	// (encryption, compression). The codec chain configured on the Temporal
	// client runs once on the signal/update/query envelope that carries each batch,
	// so encoding items here too would double-encode them; the
	// []PayloadConverter type makes that mistake impossible. To decode
	// subscribed items, use a converter built from the same PayloadConverters.
	//
	// Default: the converters from converter.GetDefaultDataConverter().
	PayloadConverters []converter.PayloadConverter
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
	// Build a codec-free converter for per-item serialization. A composite of
	// PayloadConverters cannot apply a payload codec, so items are never
	// double-encoded against the codec on the client's envelope.
	var dc converter.DataConverter = converter.GetDefaultDataConverter()
	if len(opts.PayloadConverters) > 0 {
		dc = converter.NewCompositeDataConverter(opts.PayloadConverters...)
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
// workflow reaches a terminal state after querying its remaining retained items,
// and automatically follows continue-as-new chains. Closed reads require a compatible
// worker and retained history. Query failures propagate to the subscriber.
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
		var terminalRunID string

		for {
			if err := ctx.Err(); err != nil {
				yield(WorkflowStreamItem{}, err)
				return
			}

			var result PollResult
			var err error
			if terminalRunID != "" {
				var value converter.EncodedValue
				value, err = c.c.QueryWorkflow(ctx, c.workflowID, terminalRunID, PollQueryName,
					PollInput{Topics: topics, FromOffset: offset})
				if err == nil {
					err = value.Get(&result)
				}
				if err != nil {
					yield(WorkflowStreamItem{}, err)
					return
				}
			} else {
				// Capture only this attempt's accepted run. A failed admission after
				// continue-as-new must describe the current target, not an older run.
				var polledRunID string
				var handle client.WorkflowUpdateHandle
				handle, err = c.c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
					WorkflowID:   c.workflowID,
					UpdateName:   PollUpdateName,
					Args:         []any{PollInput{Topics: topics, FromOffset: offset}},
					WaitForStage: client.WorkflowUpdateStageAccepted,
				})
				if err == nil {
					polledRunID = handle.RunID()
					err = handle.Get(ctx, &result)
				}
				if err != nil && ctx.Err() != nil {
					yield(WorkflowStreamItem{}, ctx.Err())
					return
				}

				if err != nil {
					var appErr *temporal.ApplicationError
					if errors.As(err, &appErr) {
						switch appErr.Type() {
						case ErrTypeTruncatedOffset:
							// Fell behind truncation; restart from the beginning of
							// whatever still exists.
							offset = 0
							continue
						case ErrTypeStreamDraining:
							// The workflow is detaching for continue-as-new. Back off
							// and retry; the poll lands on the successor run once the
							// rollover completes (or the chain/terminal checks below
							// fire on a genuine end).
							select {
							case <-time.After(pollCooldown):
								continue
							case <-ctx.Done():
								yield(WorkflowStreamItem{}, ctx.Err())
								return
							}
						}
					}
					// Describe the attempted run once, then pin every fallback query to
					// the confirmed execution so workflow ID reuse cannot change pages.
					desc, describeErr := c.c.DescribeWorkflowExecution(ctx, c.workflowID, polledRunID)
					if describeErr != nil {
						yield(WorkflowStreamItem{}, describeErr)
						return
					}
					info := desc.GetWorkflowExecutionInfo()
					switch info.GetStatus() {
					case enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
						if c.followCAN {
							continue
						}
					case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
						enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
						enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
						enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
						enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
						terminalRunID = info.GetExecution().GetRunId()
						if terminalRunID != "" {
							continue
						}
					}
					yield(WorkflowStreamItem{}, err)
					return
				}
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
				if terminalRunID != "" {
					return
				}
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
