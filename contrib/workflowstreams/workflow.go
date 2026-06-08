package workflowstreams

import (
	"fmt"
	"maps"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// internalEntry is a single decoded log entry held in workflow memory.
type internalEntry struct {
	topic   string
	payload *commonpb.Payload
}

// WorkflowStream is the workflow-side stream object: an append-only log served
// to external publishers (via signal), subscribers (via update), and offset
// queries (via query).
//
// Construct it once at the start of your workflow function with NewWorkflowStream. The
// constructor registers all three handlers on the current workflow.
type WorkflowStream struct {
	ctx workflow.Context
	dc  converter.DataConverter

	log               []internalEntry
	baseOffset        int64
	publisherSeqs     map[string]int64
	publisherLastSeen map[string]float64
	draining          bool

	topicHandles map[string]*WorkflowTopicHandle
}

// WorkflowStreamOption configures a WorkflowStream at construction.
type WorkflowStreamOption func(*workflowStreamConfig)

type workflowStreamConfig struct {
	payloadConverters []converter.PayloadConverter
}

// WithPayloadConverters customizes how values published from workflow code (via
// WorkflowTopicHandle.Publish) are serialized into per-item Payloads. They are
// combined into a CompositeDataConverter in the order given, so the last one
// should be a catch-all such as converter.NewJSONPayloadConverter.
//
// As on the client side, only payload conversion happens here — never a payload
// codec. The worker's codec chain runs once on the poll-update response that
// carries each batch to subscribers, so encoding items here too would
// double-encode them; the []PayloadConverter type makes that impossible.
//
// Note: there is no public accessor for a converter set via
// workflow.WithDataConverter, so it cannot be picked up automatically; pass the
// matching payload converters here to keep workflow-side publishes consistent
// with the rest of your workflow. Default: converter.GetDefaultDataConverter().
func WithPayloadConverters(pcs ...converter.PayloadConverter) WorkflowStreamOption {
	return func(c *workflowStreamConfig) { c.payloadConverters = pcs }
}

// NewWorkflowStream constructs a WorkflowStream and registers its signal, update, and
// query handlers on the current workflow. Pass priorState (which may be nil) to
// restore state carried across a continue-as-new boundary.
func NewWorkflowStream(ctx workflow.Context, priorState *WorkflowStreamState, opts ...WorkflowStreamOption) (*WorkflowStream, error) {
	var cfg workflowStreamConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	// A composite of PayloadConverters is codec-free, so workflow-published
	// items are never double-encoded against the worker's response codec.
	var dc converter.DataConverter = converter.GetDefaultDataConverter()
	if len(cfg.payloadConverters) > 0 {
		dc = converter.NewCompositeDataConverter(cfg.payloadConverters...)
	}

	s := &WorkflowStream{
		ctx:               ctx,
		dc:                dc,
		publisherSeqs:     map[string]int64{},
		publisherLastSeen: map[string]float64{},
		topicHandles:      map[string]*WorkflowTopicHandle{},
	}

	if priorState != nil {
		s.baseOffset = priorState.BaseOffset
		for _, item := range priorState.Log {
			payload, err := decodePayloadWire(item.Data)
			if err != nil {
				return nil, fmt.Errorf("workflowstreams: restore log: %w", err)
			}
			s.log = append(s.log, internalEntry{topic: item.Topic, payload: payload})
		}
		maps.Copy(s.publisherSeqs, priorState.PublisherSeqs)
		maps.Copy(s.publisherLastSeen, priorState.PublisherLastSeen)
	}

	// Signals are modeled as channels in Go; drain the publish channel from a
	// dedicated workflow coroutine.
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, PublishSignalName)
		for {
			var input PublishInput
			if !ch.Receive(ctx, &input) {
				return // channel closed (workflow completing)
			}
			s.onPublish(input)
		}
	})

	if err := workflow.SetUpdateHandlerWithOptions(ctx, PollUpdateName, s.onPoll, workflow.UpdateHandlerOptions{
		Validator: func(_ PollInput) error {
			if s.draining {
				return temporal.NewApplicationError(
					"workflow is draining for continue-as-new", ErrTypeStreamDraining)
			}
			return nil
		},
	}); err != nil {
		return nil, err
	}

	if err := workflow.SetQueryHandler(ctx, OffsetQueryName, func() (int64, error) {
		return s.baseOffset + int64(len(s.log)), nil
	}); err != nil {
		return nil, err
	}

	return s, nil
}

// Topic returns a handle for publishing to name. Repeated calls with the same
// name return the same handle.
func (s *WorkflowStream) Topic(name string) *WorkflowTopicHandle {
	if h, ok := s.topicHandles[name]; ok {
		return h
	}
	h := &WorkflowTopicHandle{name: name, stream: s}
	s.topicHandles[name] = h
	return h
}

// DetachPollers unblocks all waiting poll handlers and rejects new polls. Used
// before continue-as-new.
func (s *WorkflowStream) DetachPollers() {
	s.draining = true
}

// GetState returns a serializable snapshot of stream state for continue-as-new.
// It drops per-publisher sequence tracking for publishers that have not sent a
// batch within publisherTTL.
func (s *WorkflowStream) GetState(publisherTTL time.Duration) (*WorkflowStreamState, error) {
	now := float64(workflow.Now(s.ctx).Unix())
	ttlSeconds := publisherTTL.Seconds()

	seqs := map[string]int64{}
	seen := map[string]float64{}
	for id, seq := range s.publisherSeqs {
		ts := s.publisherLastSeen[id]
		if now-ts < ttlSeconds {
			seqs[id] = seq
			seen[id] = ts
		}
	}

	log := make([]WireItem, 0, len(s.log))
	for _, entry := range s.log {
		data, err := encodePayloadWire(entry.payload)
		if err != nil {
			return nil, err
		}
		// Per-item offset is re-derivable from baseOffset + index on reload.
		log = append(log, WireItem{Topic: entry.topic, Data: data, Offset: 0})
	}

	return &WorkflowStreamState{
		Log:               log,
		BaseOffset:        s.baseOffset,
		PublisherSeqs:     seqs,
		PublisherLastSeen: seen,
	}, nil
}

// ContinueAsNew drains pollers, waits for in-flight handlers to finish, then
// returns a continue-as-new error for wfn. buildArgs receives the post-detach
// stream state and returns the positional arguments for the new run; thread the
// returned *WorkflowStreamState into your workflow input so the stream survives.
//
// Returning the result from your workflow function ends the current run:
//
//	return stream.ContinueAsNew(ctx, MyWorkflow, func(state *workflowstreams.WorkflowStreamState) []any {
//		return []any{state}
//	})
//
// State is captured with the default 15-minute publisher TTL. For a custom TTL,
// use the manual recipe: DetachPollers, Await(AllHandlersFinished), GetState,
// then workflow.NewContinueAsNewError.
func (s *WorkflowStream) ContinueAsNew(ctx workflow.Context, wfn any, buildArgs func(state *WorkflowStreamState) []any) error {
	s.DetachPollers()
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return err
	}
	state, err := s.GetState(defaultPublisherTTL)
	if err != nil {
		return err
	}
	return workflow.NewContinueAsNewError(ctx, wfn, buildArgs(state)...)
}

// Truncate discards log entries before upToOffset and advances the base offset.
// After truncation, polls requesting an offset before the new base receive a
// TruncatedOffset error. Truncate returns a non-retryable TruncateOutOfRange
// ApplicationError if upToOffset is past the end of the log.
func (s *WorkflowStream) Truncate(upToOffset int64) error {
	logIndex := upToOffset - s.baseOffset
	if logIndex <= 0 {
		return nil
	}
	if logIndex > int64(len(s.log)) {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("cannot truncate to offset %d: only %d items exist", upToOffset, s.baseOffset+int64(len(s.log))),
			ErrTypeTruncateOutOfRange, nil)
	}
	s.log = append([]internalEntry(nil), s.log[logIndex:]...)
	s.baseOffset = upToOffset
	return nil
}

func (s *WorkflowStream) onPublish(input PublishInput) {
	if input.PublisherID != "" {
		if input.Sequence <= s.publisherSeqs[input.PublisherID] {
			return // duplicate — skip
		}
		s.publisherSeqs[input.PublisherID] = input.Sequence
		s.publisherLastSeen[input.PublisherID] = float64(workflow.Now(s.ctx).Unix())
	}
	for _, entry := range input.Items {
		payload, err := decodePayloadWire(entry.Data)
		if err != nil {
			// A malformed entry would be a protocol violation; skip it rather
			// than corrupting the log.
			continue
		}
		s.log = append(s.log, internalEntry{topic: entry.Topic, payload: payload})
	}
}

func (s *WorkflowStream) onPoll(ctx workflow.Context, input PollInput) (PollResult, error) {
	// Wait until items at or after the requested offset are available, the
	// requested offset has been truncated away, or the stream is draining.
	// baseOffset can advance via Truncate while we wait, so re-evaluate the
	// requested position against the current baseOffset on every check rather
	// than capturing it once up front — otherwise a truncation that passes the
	// waiting offset leaves the condition permanently unsatisfiable.
	truncated := false
	if err := workflow.Await(ctx, func() bool {
		if s.draining {
			return true
		}
		if input.FromOffset != 0 && input.FromOffset < s.baseOffset {
			// The subscriber's position was truncated, possibly while waiting.
			truncated = true
			return true
		}
		// max clamps "from the beginning" to whatever is available.
		logOffset := max(input.FromOffset-s.baseOffset, 0)
		return int64(len(s.log)) > logOffset
	}); err != nil {
		return PollResult{}, err
	}
	if truncated {
		return PollResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("requested offset %d has been truncated; current base offset is %d", input.FromOffset, s.baseOffset),
			ErrTypeTruncatedOffset, nil)
	}

	// max clamps "From the beginning" to whatever is available.
	logOffset := max(input.FromOffset-s.baseOffset, 0)

	var topicSet map[string]struct{}
	if len(input.Topics) > 0 {
		topicSet = make(map[string]struct{}, len(input.Topics))
		for _, t := range input.Topics {
			topicSet[t] = struct{}{}
		}
	}

	wireItems := make([]WireItem, 0)
	size := 0
	moreReady := false
	nextOffset := s.baseOffset + int64(len(s.log))

	for i := logOffset; i < int64(len(s.log)); i++ {
		entry := s.log[i]
		if topicSet != nil {
			if _, ok := topicSet[entry.topic]; !ok {
				continue
			}
		}
		globalOffset := s.baseOffset + i
		encoded, err := encodePayloadWire(entry.payload)
		if err != nil {
			return PollResult{}, err
		}
		itemSize := payloadWireSize(encoded, entry.topic)
		if size+itemSize > maxPollResponseBytes && len(wireItems) > 0 {
			// Resume from this item on the next poll.
			nextOffset = globalOffset
			moreReady = true
			break
		}
		size += itemSize
		wireItems = append(wireItems, WireItem{Topic: entry.topic, Data: encoded, Offset: globalOffset})
	}

	return PollResult{Items: wireItems, NextOffset: nextOffset, MoreReady: moreReady}, nil
}

// WorkflowTopicHandle publishes to a single topic from workflow code.
type WorkflowTopicHandle struct {
	name   string
	stream *WorkflowStream
}

// Name returns the topic name.
func (h *WorkflowTopicHandle) Name() string { return h.name }

// Publish appends value to the stream on this topic. value is serialized by the
// stream's PayloadConverters (see WithPayloadConverters), defaulting to the
// standard set; a pre-built *commonpb.Payload bypasses conversion.
func (h *WorkflowTopicHandle) Publish(value any) error {
	payload, ok := value.(*commonpb.Payload)
	if !ok {
		var err error
		payload, err = h.stream.dc.ToPayload(value)
		if err != nil {
			return fmt.Errorf("workflowstreams: convert value: %w", err)
		}
	}
	h.stream.log = append(h.stream.log, internalEntry{topic: h.name, payload: payload})
	return nil
}
