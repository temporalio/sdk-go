package internal

import (
	"context"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
	"sync/atomic"
	"time"
)

// sharedNamespaceWorker is the background nexus worker that handles heartbeating for
// all workers in a specific namespace for a specific client.
type sharedNamespaceWorker struct {
	client    *WorkflowClient
	namespace string
	taskQueue string // temporal-sys/worker-commands/{namespace}/{workerGroupingKey}
	interval  time.Duration
	logger    log.Logger

	nexusWorker *nexusWorker

	mu        sync.RWMutex
	callbacks map[string]func() *workerpb.WorkerHeartbeat // workerInstanceKey -> callback

	stopC    chan struct{}
	stoppedC chan struct{}
	started  atomic.Bool
}

func (hw *sharedNamespaceWorker) createNexusWorker() (*nexusWorker, error) {
	tuner, err := NewFixedSizeTuner(FixedSizeTunerOptions{
		NumNexusSlots: 5})
	if err != nil {
		return nil, err
	}

	params := workerExecutionParameters{
		Namespace:               hw.namespace,
		TaskQueue:               hw.taskQueue,
		Tuner:                   tuner,
		NexusTaskPollerBehavior: NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: 1}),
	}

	nw, err := newNexusWorker(nexusWorkerOptions{
		executionParameters: params,
		client:              hw.client,
		workflowService:     hw.client.workflowService,
	})

	return nw, nil
}

func (hw *sharedNamespaceWorker) run() {
	defer close(hw.stoppedC)

	hw.started.Store(true)

	if err := hw.nexusWorker.Start(); err != nil {
		return
	}
	defer hw.nexusWorker.Stop()

	ticker := time.NewTicker(hw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hw.sendHeartbeats()
		case <-hw.stopC:
			return
		}
	}
}

func (hw *sharedNamespaceWorker) sendHeartbeats() {
	hw.mu.RLock()
	callbacks := make([]func() *workerpb.WorkerHeartbeat, 0, len(hw.callbacks))
	for _, cb := range hw.callbacks {
		callbacks = append(callbacks, cb)
	}
	hw.mu.RUnlock()

	if len(callbacks) == 0 {
		return
	}

	heartbeats := make([]*workerpb.WorkerHeartbeat, 0, len(callbacks))
	for _, cb := range callbacks {
		hb := cb()
		heartbeats = append(heartbeats, hb)
	}

	if len(heartbeats) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := hw.client.RecordWorkerHeartbeat(ctx, &workflowservice.RecordWorkerHeartbeatRequest{
		Namespace:       hw.namespace,
		WorkerHeartbeat: heartbeats,
	})

	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			// Server doesn't support heartbeats, shutdown worker
			hw.stop()
			return
		}
		hw.logger.Warn("Failed to send heartbeat", "Error", err)
	}
}

func (hw *sharedNamespaceWorker) registerCallback(
	workerInstanceKey string,
	callback func() *workerpb.WorkerHeartbeat,
) {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	hw.callbacks[workerInstanceKey] = callback
}

func (hw *sharedNamespaceWorker) unregisterCallback(workerInstanceKey string) {
	shouldStop := hw.client.unregisterHeartbeatCallback(hw.namespace, workerInstanceKey)
	if shouldStop {
		hw.stop()
	}
}

func (hw *sharedNamespaceWorker) stop() {
	if !hw.started.CompareAndSwap(true, false) {
		return
	}

	close(hw.stopC)
	<-hw.stoppedC
}
