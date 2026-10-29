package lambdaworker

import (
	"context"
	"sync"
	"time"

	"go.temporal.io/sdk/worker"
)

// taskCounter tracks the tasks a worker has in hand, across all of its slot suppliers.
type taskCounter struct {
	mu sync.Mutex
	// used holds permits that have been marked used and not yet released. Release is called for
	// every reserved permit, including those whose poll returned no task, so only permits recorded
	// here count as tasks in hand.
	used map[*worker.SlotPermit]struct{}
	// totalStarted only ever increases, so that a task that starts and finishes between two
	// snapshots still shows up as activity.
	totalStarted uint64
}

func newTaskCounter() *taskCounter {
	return &taskCounter{used: make(map[*worker.SlotPermit]struct{})}
}

func (c *taskCounter) markUsed(permit *worker.SlotPermit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.used[permit]; !ok {
		c.used[permit] = struct{}{}
		c.totalStarted++
	}
}

func (c *taskCounter) release(permit *worker.SlotPermit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.used, permit)
}

func (c *taskCounter) snapshot() (used int, totalStarted uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.used), c.totalStarted
}

// wrap returns a slot supplier that forwards to inner and records task starts and finishes in c.
// It returns nil if inner is nil.
func (c *taskCounter) wrap(inner worker.SlotSupplier) worker.SlotSupplier {
	if inner == nil {
		return nil
	}
	s := &countingSlotSupplier{inner: inner, counter: c}
	// The worker looks for HasSysInfoProvider on the workflow task slot supplier to report
	// CPU and memory usage in worker heartbeats, so it must not be hidden by the wrapper.
	if sip, ok := inner.(worker.HasSysInfoProvider); ok {
		return &countingSysInfoSlotSupplier{countingSlotSupplier: s, HasSysInfoProvider: sip}
	}
	return s
}

type countingSlotSupplier struct {
	inner   worker.SlotSupplier
	counter *taskCounter
}

func (s *countingSlotSupplier) ReserveSlot(
	ctx context.Context,
	info worker.SlotReservationInfo,
) (*worker.SlotPermit, error) {
	return s.inner.ReserveSlot(ctx, info)
}

func (s *countingSlotSupplier) TryReserveSlot(info worker.SlotReservationInfo) *worker.SlotPermit {
	return s.inner.TryReserveSlot(info)
}

func (s *countingSlotSupplier) MarkSlotUsed(info worker.SlotMarkUsedInfo) {
	s.counter.markUsed(info.Permit())
	s.inner.MarkSlotUsed(info)
}

func (s *countingSlotSupplier) ReleaseSlot(info worker.SlotReleaseInfo) {
	s.inner.ReleaseSlot(info)
	s.counter.release(info.Permit())
}

func (s *countingSlotSupplier) MaxSlots() int {
	return s.inner.MaxSlots()
}

type countingSysInfoSlotSupplier struct {
	*countingSlotSupplier
	worker.HasSysInfoProvider
}

// newCountingTuner returns a tuner that wraps each of base's slot suppliers, including the
// session activity supplier, and a counter that tracks tasks in hand across all of them.
func newCountingTuner(base worker.WorkerTuner) (worker.WorkerTuner, *taskCounter, error) {
	counter := newTaskCounter()
	tuner, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier:        counter.wrap(base.GetWorkflowTaskSlotSupplier()),
		ActivitySlotSupplier:        counter.wrap(base.GetActivityTaskSlotSupplier()),
		LocalActivitySlotSupplier:   counter.wrap(base.GetLocalActivitySlotSupplier()),
		NexusSlotSupplier:           counter.wrap(base.GetNexusSlotSupplier()),
		SessionActivitySlotSupplier: counter.wrap(base.GetSessionActivitySlotSupplier()),
	})
	if err != nil {
		return nil, nil, err
	}
	return tuner, counter, nil
}

// fixedSizeTunerFromLimits builds the same fixed-size tuner the worker would build from opts'
// MaxConcurrent*ExecutionSize options when no tuner is set.
func fixedSizeTunerFromLimits(opts worker.Options) (worker.WorkerTuner, error) {
	return worker.NewFixedSizeTuner(worker.FixedSizeTunerOptions{
		NumWorkflowSlots:      opts.MaxConcurrentWorkflowTaskExecutionSize,
		NumActivitySlots:      opts.MaxConcurrentActivityExecutionSize,
		NumLocalActivitySlots: opts.MaxConcurrentLocalActivityExecutionSize,
		NumNexusSlots:         opts.MaxConcurrentNexusTaskExecutionSize,
	})
}

func clearConcurrencyLimits(opts *worker.Options) {
	opts.MaxConcurrentWorkflowTaskExecutionSize = 0
	opts.MaxConcurrentActivityExecutionSize = 0
	opts.MaxConcurrentLocalActivityExecutionSize = 0
	opts.MaxConcurrentNexusTaskExecutionSize = 0
}

// usesResourceBasedSupplier reports whether any of t's slot suppliers is resource-based. Idle
// shutdown is disabled for such tuners to keep behavior consistent with the Lambda worker packages
// in other SDKs, which can't wrap resource-based suppliers.
func usesResourceBasedSupplier(t worker.WorkerTuner) bool {
	for _, s := range []worker.SlotSupplier{
		t.GetWorkflowTaskSlotSupplier(),
		t.GetActivityTaskSlotSupplier(),
		t.GetLocalActivitySlotSupplier(),
		t.GetNexusSlotSupplier(),
		t.GetSessionActivitySlotSupplier(),
	} {
		if _, ok := s.(*worker.ResourceBasedSlotSupplier); ok {
			return true
		}
	}
	return false
}

// idleThreshold returns how long waitForIdle should wait for a configured idle timeout. jitter
// returns a random duration in [0, n).
func idleThreshold(idleTimeout time.Duration, jitter func(n time.Duration) time.Duration) time.Duration {
	// waitForIdle measures idleness from the last check that saw a task in hand, which can be up
	// to one check interval before the task finished, so add one interval to guarantee at least
	// idleTimeout of actual idleness.
	threshold := idleTimeout + idleCheckInterval
	// Workers that finish similar tasks at the same time, e.g. after a burst of invocations, would
	// otherwise all shut down at once.
	if maxJitter := time.Duration(float64(idleTimeout) * maxIdleTimeoutJitterFraction); maxJitter > 0 {
		threshold += jitter(maxJitter)
	}
	return threshold
}

// waitForIdle blocks until counter has shown no task in hand and no new task started for
// threshold, and returns true, or until ctx is done, and returns false. The idle clock starts
// when waitForIdle is called.
func waitForIdle(ctx context.Context, counter *taskCounter, threshold time.Duration) bool {
	ticker := time.NewTicker(idleCheckInterval)
	defer ticker.Stop()
	idleSince := time.Now()
	_, lastTotal := counter.snapshot()
	for {
		select {
		case <-ctx.Done():
			return false
		case now := <-ticker.C:
			used, total := counter.snapshot()
			if used > 0 || total != lastTotal {
				idleSince = now
				lastTotal = total
			} else if now.Sub(idleSince) >= threshold {
				return true
			}
		}
	}
}
