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

	"go.temporal.io/sdk/internal/common/metrics"

	"golang.org/x/sync/semaphore"
)

// WorkerTuner allows for the dynamic customization of some aspects of worker behavior.
type WorkerTuner interface {
	GetWorkflowTaskSlotSupplier() SlotSupplier
	GetActivityTaskSlotSupplier() SlotSupplier
	GetLocalActivitySlotSupplier() SlotSupplier
}

// CompositeTuner allows you to build a tuner from multiple slot suppliers.
type CompositeTuner struct {
	workflowSlotSupplier      SlotSupplier
	activitySlotSupplier      SlotSupplier
	localActivitySlotSupplier SlotSupplier
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

func CreateFixedSizeTuner(numWorkflowSlots, numActivitySlots, numLocalActivitySlots int) WorkerTuner {
	return &CompositeTuner{
		workflowSlotSupplier:      NewFixedSizeSlotSupplier(numWorkflowSlots),
		activitySlotSupplier:      NewFixedSizeSlotSupplier(numActivitySlots),
		localActivitySlotSupplier: NewFixedSizeSlotSupplier(numLocalActivitySlots),
	}
}

func CreateCompositeTuner(workflowSlotSupplier, activitySlotSupplier, localActivitySlotSupplier SlotSupplier) WorkerTuner {
	return &CompositeTuner{
		workflowSlotSupplier:      workflowSlotSupplier,
		activitySlotSupplier:      activitySlotSupplier,
		localActivitySlotSupplier: localActivitySlotSupplier,
	}
}

type SlotPermit struct {
	//lint:ignore U1000 pointless to guarantee uniqueness for now
	int
}

type SlotReserveContext interface {
	TaskQueue() string
	NumIssuedSlots() int
}

// SlotSupplier controls how slots are handed out for workflow and activity tasks as well as
// local activities when used in conjunction with a WorkerTuner.
//
// Currently, you cannot implement your own slot supplier. You can use the provided
// FixedSizeSlotSupplier and ResourceBasedSlotSupplier slot suppliers.
type SlotSupplier interface {
	// ReserveSlot is called before polling for new tasks. The implementation should block until
	// a slot is available, then return a permit to use that slot. Implementations must be
	// thread-safe.
	//
	// Any returned error besides context.Canceled will be logged and the function will be retried.
	ReserveSlot(ctx context.Context, reserveCtx SlotReserveContext) (*SlotPermit, error)
	// TryReserveSlot is called when attempting to reserve slots for eager workflows and activities.
	// It should return a permit if a slot is available, and nil otherwise. Implementations must be
	// thread-safe.
	TryReserveSlot(reserveCtx SlotReserveContext) *SlotPermit

	MarkSlotUsed()

	ReleaseSlot()

	// MaximumSlots returns the maximum number of slots that this supplier will ever issue.
	// Implementations may return 0 if there is no well-defined upper limit. In such cases the
	// available task slots metric will not be emitted.
	MaximumSlots() int
}

// FixedSizeSlotSupplier is a slot supplier that will only ever issue at most a fixed number of
// slots.
type FixedSizeSlotSupplier struct {
	// The maximum number of slots that this supplier will ever issue.
	NumSlots int

	sem *semaphore.Weighted
}

func NewFixedSizeSlotSupplier(numSlots int) *FixedSizeSlotSupplier {
	return &FixedSizeSlotSupplier{
		NumSlots: numSlots,
		sem:      semaphore.NewWeighted(int64(numSlots)),
	}
}

func (f *FixedSizeSlotSupplier) ReserveSlot(ctx context.Context, reserveCtx SlotReserveContext) (*SlotPermit, error) {
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
func (f *FixedSizeSlotSupplier) MarkSlotUsed() {}
func (f *FixedSizeSlotSupplier) ReleaseSlot() {
	f.sem.Release(1)
}
func (f *FixedSizeSlotSupplier) MaximumSlots() int {
	return f.NumSlots
}

type slotReservationData struct {
	taskQueue string
}

type slotReserveContextImpl struct {
	taskQueue   string
	issuedSlots int
}

func (s slotReserveContextImpl) TaskQueue() string {
	return s.taskQueue
}

func (s slotReserveContextImpl) NumIssuedSlots() int {
	return s.issuedSlots
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
	permit, err := t.inner.ReserveSlot(ctx, slotReserveContextImpl{
		taskQueue:   data.taskQueue,
		issuedSlots: int(t.issuedSlotsAtomic.Load()),
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
	})
	if permit != nil {
		t.issuedSlotsAtomic.Add(1)
		t.publishMetrics(false)
	}
	return permit
}
func (t *trackingSlotSupplier) MarkSlotUsed(permit *SlotPermit) {
	if permit == nil {
		return
	}
	t.slotsMutex.Lock()
	defer t.slotsMutex.Unlock()
	t.usedSlots[permit] = struct{}{}
	t.inner.MarkSlotUsed()
	t.publishMetrics(true)
}
func (t *trackingSlotSupplier) ReleaseSlot(permit *SlotPermit, reason string) {
	if permit == nil {
		panic("Cannot release with nil permit")
	}
	t.slotsMutex.Lock()
	defer t.slotsMutex.Unlock()
	_ = t.usedSlots[permit]
	t.inner.ReleaseSlot()
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
	if t.inner.MaximumSlots() != 0 {
		t.taskSlotsAvailableGauge.Update(float64(t.inner.MaximumSlots() - usedSlots))
	}
	t.taskSlotsUsedGauge.Update(float64(usedSlots))
}
