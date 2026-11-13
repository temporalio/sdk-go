package lambdaworker

import (
	"context"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

const (
	// How often waitForIdle re-reads slot usage while tasks are in progress, since nothing signals
	// when they finish.
	idleCheckInterval = time.Second
	// Leaves a margin over the brief window between a poll returning a task and its slot being marked
	// used, during which the worker looks idle.
	minIdleTimeout               = 5 * time.Second
	maxIdleTimeoutJitterFraction = 0.2
)

// idleThreshold returns how long the worker must be idle before shutting down, for an idle timeout
// of at least minIdleTimeout. jitter must return a random duration in [0, n).
func idleThreshold(idleTimeout time.Duration, jitter func(n time.Duration) time.Duration) time.Duration {
	// Workers that finish similar tasks at the same time, e.g. after a burst of invocations, would
	// otherwise all shut down at once.
	return idleTimeout + jitter(time.Duration(float64(idleTimeout)*maxIdleTimeoutJitterFraction))
}

// startIdleMonitor runs [waitForIdle] in a goroutine, closing idleC once the worker is idle. Call
// stop to end the monitor.
func startIdleMonitor(
	getUsage func() (worker.SlotUsage, error),
	threshold time.Duration,
	logger log.Logger,
) (idleC <-chan struct{}, stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := waitForIdle(ctx, getUsage, threshold)
		if err == nil {
			close(idle)
		} else if ctx.Err() == nil && logger != nil {
			logger.Warn("Idle shutdown disabled for this invocation", "error", err)
		}
	}()
	return idle, func() {
		cancel()
		<-done
	}
}

// waitForIdle blocks until the worker has been idle for threshold, or returns an error if ctx is
// done or getUsage fails.
func waitForIdle(
	ctx context.Context,
	getUsage func() (worker.SlotUsage, error),
	threshold time.Duration,
) error {
	start := time.Now()
	for {
		usage, err := getUsage()
		if err != nil {
			return err
		}
		wait := idleCheckInterval
		if !hasTasksInProgress(usage) {
			idleSince := lastUsageChange(usage)
			if idleSince.Before(start) {
				idleSince = start
			}
			wait = threshold - time.Since(idleSince)
			if wait <= 0 {
				return nil
			}
			// If a task starts in the meantime, the next read sees it, either still in progress or as
			// a later change.
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func slotKindUsages(u worker.SlotUsage) []worker.SlotKindUsage {
	return []worker.SlotKindUsage{u.Workflow, u.Activity, u.LocalActivity, u.Nexus}
}

func hasTasksInProgress(u worker.SlotUsage) bool {
	for _, k := range slotKindUsages(u) {
		if k.UsedSlots > 0 {
			return true
		}
	}
	return false
}

func lastUsageChange(u worker.SlotUsage) time.Time {
	var last time.Time
	for _, k := range slotKindUsages(u) {
		if k.UsedSlotsLastChanged.After(last) {
			last = k.UsedSlotsLastChanged
		}
	}
	return last
}
