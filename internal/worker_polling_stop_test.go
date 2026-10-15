// These tests hold a polling slot or its rate wait while polling is disabled.
// They check that no request starts and that unused slots and polling capacity
// are returned, while existing shutdown tests cover tasks already accepted.
package internal

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"golang.org/x/time/rate"
)

type (
	// pollingHeldSlotSupplier delays a slot reservation until the test releases
	// it, allowing polling to be disabled before the reserved slot is returned.
	pollingHeldSlotSupplier struct {
		SlotSupplier
		entered chan struct{}
		release chan struct{}
	}
)

func TestWorkerPollingStopReturnsUnusedReservations(t *testing.T) {
	for _, autoscaling := range []bool{false, true} {
		for _, wait := range []string{"slot", "rate"} {
			name := map[bool]string{false: "fixed", true: "autoscaling"}[autoscaling] + "/" + wait
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					recorded := &releaseRecordingSlotSupplier{released: make(chan SlotReleaseReason, 2)}
					supplier := &pollingHeldSlotSupplier{
						SlotSupplier: recorded,
						entered:      make(chan struct{}),
						release:      make(chan struct{}),
					}
					producer := newBlockingProbeTaskPoller()
					opts := lifecycleOptions(autoscaling, "activity")
					opts.ActivityTaskPollerBehavior = NewPollerBehaviorSimpleMaximum(
						PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: 1},
					)
					if autoscaling {
						opts.ActivityTaskPollerBehavior = NewPollerBehaviorAutoscaling(
							PollerBehaviorAutoscalingOptions{
								InitialNumberOfPollers: 1,
								MinimumNumberOfPollers: 1,
								MaximumNumberOfPollers: 1,
							},
						)
					}
					poller := newScalableTaskPoller(
						producer, ilog.NewNopLogger(), opts.ActivityTaskPollerBehavior,
						metrics.PollerTypeActivityTask, nil, nil,
					)
					bw := newBaseWorker(baseWorkerOptions{
						slotSupplier:     supplier,
						maxTaskPerSecond: 1000,
						taskPollers:      []scalableTaskPoller{poller},
						taskProcessor:    noopTaskProcessor{},
						workerType:       "PollingStopTest",
						logger:           ilog.NewNopLogger(),
						stopTimeout:      time.Minute,
						metricsHandler:   metrics.NopHandler,
					})
					if wait == "rate" {
						bw.pollLimiter = rate.NewLimiter(rate.Every(time.Second), 1)
						require.True(t, bw.pollLimiter.Allow())
					}
					bw.Start()
					defer bw.Stop()
					defer producer.Close()
					<-supplier.entered
					if wait == "rate" {
						close(supplier.release)
						synctest.Wait()
						assert.EqualValues(t, 1, bw.slotSupplier.issuedSlotsAtomic.Load())
					}
					bw.noRepoll.Store(true)
					if wait == "slot" {
						close(supplier.release)
					}
					retired := make(chan struct{})
					go func() {
						bw.pollerWG.Wait()
						close(retired)
					}()
					<-retired
					assert.Zero(t, producer.startedPolls(), "a stopped poll opened after its wait")
					assert.Zero(t, bw.slotSupplier.issuedSlotsAtomic.Load())
					assert.Equal(t, SlotReleaseReasonUnused, <-recorded.released)
					select {
					case reason := <-recorded.released:
						t.Fatalf("the unused slot was released again: %v", reason)
					default:
					}
					if autoscaling {
						poller.autoscalingRunner.activeMu.Lock()
						active := poller.autoscalingRunner.active
						poller.autoscalingRunner.activeMu.Unlock()
						assert.Zero(t, active)
					}
				})
			})
		}
	}
}

func (s *pollingHeldSlotSupplier) ReserveSlot(ctx context.Context, info SlotReservationInfo) (*SlotPermit, error) {
	close(s.entered)
	select {
	case <-s.release:
		return s.SlotSupplier.ReserveSlot(ctx, info)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
