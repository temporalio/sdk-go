package workflowstreams

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// FlushTimeoutError is returned when a flush retry exceeds MaxRetryDuration.
// The pending batch is dropped; if the signal had already been delivered the
// items are in the log, otherwise they are lost.
type FlushTimeoutError struct {
	msg string
}

func (e *FlushTimeoutError) Error() string { return e.msg }

type bufItem struct {
	topic string
	value any
}

// publisher owns the client-side publish path: it buffers published values,
// batches them, and sends each batch to the workflow via the injected signal
// function. It assigns the per-publisher dedup key (a stable publisher ID plus
// a monotonic sequence advanced only on a confirmed send) so the workflow can
// drop duplicates, and it retries a failed batch until maxRetryDur elapses.
//
// The signal func is injected (rather than holding a client.Client) so the
// publish path can be exercised in isolation.
type publisher struct {
	signal        func(ctx context.Context, in PublishInput) error
	dc            converter.DataConverter
	publisherID   string
	batchInterval time.Duration
	maxBatchSize  int
	maxRetryDur   time.Duration

	mu           sync.Mutex
	buffer       []bufItem
	pending      []PublishEntry
	pendingSeq   int64
	sequence     int64
	pendingStart time.Time
	started      bool
	closed       bool
	err          error

	flushMu sync.Mutex // serializes doFlush
	trigger chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func newPublisher(signal func(context.Context, PublishInput) error, dc converter.DataConverter, opts Options) *publisher {
	p := &publisher{
		signal:        signal,
		dc:            dc,
		publisherID:   strings.ReplaceAll(uuid.NewString(), "-", "")[:16],
		batchInterval: opts.BatchInterval,
		maxBatchSize:  opts.MaxBatchSize,
		maxRetryDur:   opts.MaxRetryDuration,
		trigger:       make(chan struct{}, 1),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	if p.batchInterval <= 0 {
		p.batchInterval = defaultBatchInterval
	}
	if p.maxRetryDur <= 0 {
		p.maxRetryDur = defaultMaxRetryDuration
	}
	return p
}

// publish buffers a value and lazily starts the background flush loop. It
// triggers an immediate flush on forceFlush or once the buffer reaches
// maxBatchSize.
func (p *publisher) publish(topic string, value any, forceFlush bool) {
	p.mu.Lock()
	p.buffer = append(p.buffer, bufItem{topic: topic, value: value})
	trigger := forceFlush || (p.maxBatchSize > 0 && len(p.buffer) >= p.maxBatchSize)
	closed := p.closed
	p.mu.Unlock()

	if !closed {
		p.ensureStarted()
	}
	if trigger {
		p.triggerFlush()
	}
}

func (p *publisher) ensureStarted() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.closed {
		return
	}
	p.started = true
	go p.run()
}

func (p *publisher) triggerFlush() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

func (p *publisher) run() {
	defer close(p.done)
	ticker := time.NewTicker(p.batchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-p.trigger:
		case <-ticker.C:
		}
		if err := p.doFlush(context.Background()); err != nil {
			var fte *FlushTimeoutError
			if errors.As(err, &fte) {
				// The pending batch was dropped and can't be recovered. Stash
				// the error so flush/close surface it and stop the loop.
				p.mu.Lock()
				p.err = err
				p.mu.Unlock()
				return
			}
			// Transient failure: pending stays set for retry on the next tick.
		}
	}
}

// doFlush sends the pending batch (retry) or encodes and sends the buffer (new
// batch). It is serialized so concurrent callers send sequentially.
func (p *publisher) doFlush(ctx context.Context) error {
	p.flushMu.Lock()
	defer p.flushMu.Unlock()

	var batch []PublishEntry
	var seq int64

	p.mu.Lock()
	switch {
	case p.pending != nil:
		if !p.pendingStart.IsZero() && time.Since(p.pendingStart) > p.maxRetryDur {
			// Advance the confirmed sequence so the next batch gets a fresh
			// sequence number. Without this the next batch reuses pendingSeq,
			// which the workflow may have already accepted — causing silent
			// dedup (data loss).
			p.sequence = p.pendingSeq
			p.pending = nil
			p.pendingSeq = 0
			p.pendingStart = time.Time{}
			p.mu.Unlock()
			return &FlushTimeoutError{msg: fmt.Sprintf(
				"workflowstreams: flush retry exceeded MaxRetryDuration (%s); pending batch dropped", p.maxRetryDur)}
		}
		batch = p.pending
		seq = p.pendingSeq
	case len(p.buffer) > 0:
		encoded, err := p.encodeBuffer(p.buffer)
		if err != nil {
			p.mu.Unlock()
			return err // buffer left intact for a later flush
		}
		p.buffer = nil
		batch = encoded
		seq = p.sequence + 1
		p.pending = batch
		p.pendingSeq = seq
		p.pendingStart = time.Now()
	default:
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// On failure the signal returns an error and pending stays set for retry.
	if err := p.signal(ctx, PublishInput{Items: batch, PublisherID: p.publisherID, Sequence: seq}); err != nil {
		return err
	}

	p.mu.Lock()
	p.sequence = seq
	p.pending = nil
	p.pendingSeq = 0
	p.pendingStart = time.Time{}
	p.mu.Unlock()
	return nil
}

func (p *publisher) encodeBuffer(items []bufItem) ([]PublishEntry, error) {
	out := make([]PublishEntry, len(items))
	for i, it := range items {
		payload, ok := it.value.(*commonpb.Payload)
		if !ok {
			var err error
			payload, err = p.dc.ToPayload(it.value)
			if err != nil {
				return nil, fmt.Errorf("workflowstreams: convert value: %w", err)
			}
		}
		data, err := encodePayloadWire(payload)
		if err != nil {
			return nil, err
		}
		out[i] = PublishEntry{Topic: it.topic, Data: data}
	}
	return out, nil
}

// flush sends buffered (and pending) items and waits for server confirmation.
// It returns once the items buffered at call time have been signaled and
// acknowledged, or a FlushTimeoutError if a pending batch cannot be sent within
// maxRetryDur.
func (p *publisher) flush(ctx context.Context) error {
	if err := p.takeError(); err != nil {
		return err
	}

	p.mu.Lock()
	if p.pending == nil && len(p.buffer) == 0 {
		p.mu.Unlock()
		return nil
	}
	baseSeq := p.sequence
	if p.pending != nil {
		baseSeq = p.pendingSeq
	}
	targetSeq := baseSeq
	if len(p.buffer) > 0 {
		targetSeq = baseSeq + 1
	}
	p.mu.Unlock()

	for {
		p.mu.Lock()
		cur := p.sequence
		p.mu.Unlock()
		if cur >= targetSeq {
			break
		}
		if err := p.doFlush(ctx); err != nil {
			return err
		}
	}
	return p.takeError()
}

// close stops the background flush loop and drains any remaining items,
// surfacing a deferred FlushTimeoutError from a prior background failure.
func (p *publisher) close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	started := p.started
	p.mu.Unlock()

	if started {
		close(p.stop)
		<-p.done
	}

	// Final drain: a single doFlush processes either pending OR the buffer.
	for {
		p.mu.Lock()
		more := p.pending != nil || len(p.buffer) > 0
		p.mu.Unlock()
		if !more {
			break
		}
		if err := p.doFlush(ctx); err != nil {
			return err
		}
	}
	return p.takeError()
}

func (p *publisher) takeError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		err := p.err
		p.err = nil
		return err
	}
	return nil
}
