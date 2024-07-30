// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/semaphore"

	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

// WorkerTuner allows for the dynamic customization of some aspects of worker behavior.
//
// WARNING: Custom implementations of SlotSupplier are currently experimental.
type WorkerTuner interface {
	GetWorkflowTaskSlotSupplier() SlotSupplier
	GetActivityTaskSlotSupplier() SlotSupplier
	GetLocalActivitySlotSupplier() SlotSupplier
	GetNexusSlotSupplier() SlotSupplier
}

// SlotPermit is a permit to use a slot.
//
// WARNING: Custom implementations of SlotSupplier are currently experimental.
type SlotPermit struct {
	//lint:ignore U1000 pointless to guarantee pointers to SlotPermits are unique
	_uniqueInt int
}

// SlotReserveContext contains information that SlotSupplier instances can use during
// reservation calls. It embeds a standard Context.
type SlotReserveContext interface {
	context.Context

	TaskQueue() string
	NumIssuedSlots() int
	Logger() log.Logger
}

// SlotMarkUsedContext contains information that SlotSupplier instances can use during
// SlotSupplier.MarkSlotUsed calls.
type SlotMarkUsedContext interface {
	// Permit returns the permit that is being marked as used.
	Permit() *SlotPermit
}

// SlotReleaseReason describes the reason that a slot is being released.
type SlotReleaseReason int

const (
	SlotReleaseReasonTaskProcessed SlotReleaseReason = iota
	SlotReleaseReasonUnused
)

// SlotReleaseContext contains information that SlotSupplier instances can use during
// SlotSupplier.ReleaseSlot calls.
type SlotReleaseContext interface {
	// Permit returns the permit that is being released.
	Permit() *SlotPermit
	// Reason returns the reason that the slot is being released.
	Reason() SlotReleaseReason
}

// SlotSupplier controls how slots are handed out for workflow and activity tasks as well as
// local activities when used in conjunction with a WorkerTuner.
//
// WARNING: Custom implementations of SlotSupplier are currently experimental.
type SlotSupplier interface {
	// ReserveSlot is called before polling for new tasks. The implementation should block until
	// a slot is available, then return a permit to use that slot. Implementations must be
	// thread-safe.
	//
	// Any returned error besides context.Canceled will be logged and the function will be retried.
	ReserveSlot(ctx SlotReserveContext) (*SlotPermit, error)

	// TryReserveSlot is called when attempting to reserve slots for eager workflows and activities.
	// It should return a permit if a slot is available, and nil otherwise. Implementations must be
	// thread-safe.
	TryReserveSlot(ctx SlotReserveContext) *SlotPermit

	// MarkSlotUsed is called once a slot is about to be used for actually processing a task.
	// Because slots are reserved before task polling, not all reserved slots will be used.
	// Implementations must be thread-safe.
	MarkSlotUsed(ctx SlotMarkUsedContext)

	// ReleaseSlot is called when a slot is no longer needed, which is typically after the task
	// has been processed, but may also be called upon shutdown or other situations where the
	// slot is no longer needed. Implementations must be thread-safe.
	ReleaseSlot(ctx SlotReleaseContext)

	// MaxSlots returns the maximum number of slots that this supplier will ever issue.
	// Implementations may return 0 if there is no well-defined upper limit. In such cases the
	// available task slots metric will not be emitted.
	MaxSlots() int
}

// CompositeTuner allows you to build a tuner from multiple slot suppliers.
//
// WARNING: Custom implementations of SlotSupplier are currently experimental.
type CompositeTuner struct {
	workflowSlotSupplier      SlotSupplier
	activitySlotSupplier      SlotSupplier
	localActivitySlotSupplier SlotSupplier
	nexusSlotSupplier         SlotSupplier
}

func (c *CompositeTuner) GetWorkflowTaskSlotSupplier() SlotSupplier {
	return c.workflowSlotSupplier
}
func (c *CompositeTuner) GetActivityTaskSlotSupplier() SlotSupplier {
	return c.activitySlotSupplier
}
func (c *CompositeTuner) GetLocalActivitySlotSupplier() SlotSupplier {
	return c.localActivitySlotSupplier
}
func (c *CompositeTuner) GetNexusSlotSupplier() SlotSupplier {
	return c.nexusSlotSupplier
}

// CompositeTunerOptions are the options used by NewCompositeTuner.
type CompositeTunerOptions struct {
	WorkflowSlotSupplier      SlotSupplier
	ActivitySlotSupplier      SlotSupplier
	LocalActivitySlotSupplier SlotSupplier
	NexusSlotSupplier         SlotSupplier
}

// NewCompositeTuner creates a WorkerTuner that uses a combination of slot suppliers.
//
// WARNING: Custom implementations of SlotSupplier are currently experimental.
func NewCompositeTuner(options CompositeTunerOptions) (WorkerTuner, error) {
	return &CompositeTuner{
		workflowSlotSupplier:      options.WorkflowSlotSupplier,
		activitySlotSupplier:      options.ActivitySlotSupplier,
		localActivitySlotSupplier: options.LocalActivitySlotSupplier,
		nexusSlotSupplier:         options.NexusSlotSupplier,
	}, nil
}

// FixedSizeTunerOptions are the options used by NewFixedSizeTuner.
type FixedSizeTunerOptions struct {
	NumWorkflowSlots      int
	NumActivitySlots      int
	NumLocalActivitySlots int
	NumNexusSlots         int
}

// NewFixedSizeTuner creates a WorkerTuner that uses fixed size slot suppliers.
func NewFixedSizeTuner(options FixedSizeTunerOptions) (WorkerTuner, error) {
	if options.NumWorkflowSlots <= 0 {
		options.NumWorkflowSlots = defaultMaxConcurrentTaskExecutionSize
	}
	if options.NumActivitySlots <= 0 {
		options.NumActivitySlots = defaultMaxConcurrentActivityExecutionSize
	}
	if options.NumLocalActivitySlots <= 0 {
		options.NumLocalActivitySlots = defaultMaxConcurrentLocalActivityExecutionSize
	}
	if options.NumNexusSlots <= 0 {
		options.NumNexusSlots = defaultMaxConcurrentTaskExecutionSize
	}
	wfSS, err := NewFixedSizeSlotSupplier(options.NumWorkflowSlots)
	if err != nil {
		return nil, err
	}
	actSS, err := NewFixedSizeSlotSupplier(options.NumActivitySlots)
	if err != nil {
		return nil, err
	}
	laSS, err := NewFixedSizeSlotSupplier(options.NumLocalActivitySlots)
	if err != nil {
		return nil, err
	}
	nexusSS, err := NewFixedSizeSlotSupplier(options.NumNexusSlots)
	if err != nil {
		return nil, err
	}
	return &CompositeTuner{
		workflowSlotSupplier:      wfSS,
		activitySlotSupplier:      actSS,
		localActivitySlotSupplier: laSS,
		nexusSlotSupplier:         nexusSS,
	}, nil
}

// FixedSizeSlotSupplier is a slot supplier that will only ever issue at most a fixed number of
// slots.
type FixedSizeSlotSupplier struct {
	// The maximum number of slots that this supplier will ever issue.
	NumSlots int

	sem *semaphore.Weighted
}

// NewFixedSizeSlotSupplier creates a new FixedSizeSlotSupplier with the given number of slots.
func NewFixedSizeSlotSupplier(numSlots int) (*FixedSizeSlotSupplier, error) {
	if numSlots <= 0 {
		return nil, fmt.Errorf("NumSlots must be positive")
	}
	return &FixedSizeSlotSupplier{
		NumSlots: numSlots,
		sem:      semaphore.NewWeighted(int64(numSlots)),
	}, nil
}

func (f *FixedSizeSlotSupplier) ReserveSlot(ctx SlotReserveContext) (*SlotPermit, error) {
	err := f.sem.Acquire(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire slot: %w", err)
	}

	return &SlotPermit{}, nil
}
func (f *FixedSizeSlotSupplier) TryReserveSlot(SlotReserveContext) *SlotPermit {
	if f.sem.TryAcquire(1) {
		return &SlotPermit{}
	}
	return nil
}
func (f *FixedSizeSlotSupplier) MarkSlotUsed(SlotMarkUsedContext) {}
func (f *FixedSizeSlotSupplier) ReleaseSlot(SlotReleaseContext) {
	f.sem.Release(1)
}
func (f *FixedSizeSlotSupplier) MaxSlots() int {
	return f.NumSlots
}

type slotReservationData struct {
	taskQueue string
	logger    log.Logger
}

type slotReserveContextImpl struct {
	context.Context
	taskQueue   string
	issuedSlots int
	logger      log.Logger
}

func (s slotReserveContextImpl) TaskQueue() string {
	return s.taskQueue
}

func (s slotReserveContextImpl) NumIssuedSlots() int {
	return s.issuedSlots
}

func (s slotReserveContextImpl) Logger() log.Logger {
	return s.logger
}

type slotMarkUsedContextImpl struct {
	permit *SlotPermit
}

func (s slotMarkUsedContextImpl) Permit() *SlotPermit {
	return s.permit
}

type slotReleaseContextImpl struct {
	permit *SlotPermit
	reason SlotReleaseReason
}

func (s slotReleaseContextImpl) Permit() *SlotPermit {
	return s.permit
}

func (s slotReleaseContextImpl) Reason() SlotReleaseReason {
	return s.reason
}

type trackingSlotSupplier struct {
	inner SlotSupplier

	issuedSlotsAtomic atomic.Int32
	slotsMutex        sync.Mutex
	// Eventually the map values will be slot info once the API is exposed
	usedSlots               map[*SlotPermit]struct{}
	taskSlotsAvailableGauge metrics.Gauge
	taskSlotsUsedGauge      metrics.Gauge
}

func newTrackingSlotSupplier(inner SlotSupplier, metricsHandler metrics.Handler) *trackingSlotSupplier {
	tss := &trackingSlotSupplier{
		inner:                   inner,
		usedSlots:               make(map[*SlotPermit]struct{}),
		taskSlotsAvailableGauge: metricsHandler.Gauge(metrics.WorkerTaskSlotsAvailable),
		taskSlotsUsedGauge:      metricsHandler.Gauge(metrics.WorkerTaskSlotsUsed),
	}
	return tss
}

func (t *trackingSlotSupplier) ReserveSlot(
	ctx context.Context,
	data *slotReservationData,
) (*SlotPermit, error) {
	permit, err := t.inner.ReserveSlot(slotReserveContextImpl{
		Context:     ctx,
		taskQueue:   data.taskQueue,
		issuedSlots: int(t.issuedSlotsAtomic.Load()),
		logger:      data.logger,
	})
	if err != nil {
		return nil, err
	}
	if permit == nil {
		return nil, fmt.Errorf("slot supplier returned nil permit")
	}
	t.issuedSlotsAtomic.Add(1)
	t.publishMetrics(false)
	return permit, nil
}

func (t *trackingSlotSupplier) TryReserveSlot(data *slotReservationData) *SlotPermit {
	permit := t.inner.TryReserveSlot(slotReserveContextImpl{
		taskQueue:   data.taskQueue,
		issuedSlots: int(t.issuedSlotsAtomic.Load()),
		logger:      data.logger,
	})
	if permit != nil {
		t.issuedSlotsAtomic.Add(1)
		t.publishMetrics(false)
	}
	return permit
}

func (t *trackingSlotSupplier) MarkSlotUsed(permit *SlotPermit) {
	if permit == nil {
		panic("Cannot mark nil permit as used")
	}
	t.slotsMutex.Lock()
	defer t.slotsMutex.Unlock()
	t.usedSlots[permit] = struct{}{}
	t.inner.MarkSlotUsed(&slotMarkUsedContextImpl{
		permit: permit,
	})
	t.publishMetrics(true)
}

func (t *trackingSlotSupplier) ReleaseSlot(permit *SlotPermit, reason SlotReleaseReason) {
	if permit == nil {
		panic("Cannot release with nil permit")
	}
	t.slotsMutex.Lock()
	defer t.slotsMutex.Unlock()
	_ = t.usedSlots[permit]
	t.inner.ReleaseSlot(&slotReleaseContextImpl{
		permit: permit,
		reason: reason,
	})
	t.issuedSlotsAtomic.Add(-1)
	delete(t.usedSlots, permit)
	t.publishMetrics(true)
}

func (t *trackingSlotSupplier) publishMetrics(lockAlreadyHeld bool) {
	if !lockAlreadyHeld {
		t.slotsMutex.Lock()
		defer t.slotsMutex.Unlock()
	}
	usedSlots := len(t.usedSlots)
	if t.inner.MaxSlots() != 0 {
		t.taskSlotsAvailableGauge.Update(float64(t.inner.MaxSlots() - usedSlots))
	}
	t.taskSlotsUsedGauge.Update(float64(usedSlots))
}
