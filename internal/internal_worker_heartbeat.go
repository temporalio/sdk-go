package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// heartbeatManager manages heartbeat workers across namespaces for a client.
type heartbeatManager struct {
	client   *WorkflowClient
	interval time.Duration
	logger   log.Logger

	mu      sync.Mutex
	workers map[string]*sharedNamespaceWorker // namespace -> worker
}

// newHeartbeatManager creates a new heartbeatManager.
func newHeartbeatManager(client *WorkflowClient, interval time.Duration, logger log.Logger) *heartbeatManager {
	return &heartbeatManager{
		client:   client,
		interval: interval,
		logger:   logger,
		workers:  make(map[string]*sharedNamespaceWorker),
	}
}

// registerWorker registers a worker's heartbeat callback with the shared heartbeat worker for the namespace.
func (m *heartbeatManager) registerWorker(
	worker *AggregatedWorker,
) error {
	m.mu.Lock()

	namespace := worker.executionParams.Namespace
	hw, ok := m.workers[namespace]
	m.mu.Unlock()
	if !ok {
		capabilities, err := m.client.loadNamespaceCapabilities(worker.heartbeatMetrics)
		if err != nil {
			return fmt.Errorf("failed to get namespace capabilities: %w", err)
		}
		if !capabilities.GetWorkerHeartbeats() {
			m.logger.Debug("Worker heartbeating configured, but server version does not support it.")
			return nil
		}

		hw = &sharedNamespaceWorker{
			client:    m.client,
			namespace: namespace,
			interval:  m.interval,
			callbacks: make(map[string]func() *workerpb.WorkerHeartbeat),
			stopC:     make(chan struct{}),
			stoppedC:  make(chan struct{}),
			logger:    m.logger,
		}

		m.mu.Lock()
		m.workers[namespace] = hw
		m.mu.Unlock()
		go hw.run()
	}

	hw.mu.Lock()
	hw.callbacks[worker.workerInstanceKey] = worker.heartbeatCallback
	hw.mu.Unlock()

	return nil
}

// unregisterWorker removes a worker's heartbeat callback. If no callbacks remain for the namespace,
// the shared heartbeat worker is stopped.
func (m *heartbeatManager) unregisterWorker(worker *AggregatedWorker) {
	m.mu.Lock()
	defer m.mu.Unlock()

	namespace := worker.executionParams.Namespace
	hw, ok := m.workers[namespace]
	if !ok {
		return
	}

	hw.mu.Lock()
	delete(hw.callbacks, worker.workerInstanceKey)
	remaining := len(hw.callbacks)
	hw.mu.Unlock()

	if remaining == 0 {
		hw.stop()
		delete(m.workers, namespace)
	}
}

// sharedNamespaceWorker handles heartbeating for all workers in a specific namespace for a specific client.
type sharedNamespaceWorker struct {
	client    *WorkflowClient
	namespace string
	interval  time.Duration
	logger    log.Logger

	mu        sync.RWMutex
	callbacks map[string]func() *workerpb.WorkerHeartbeat // workerInstanceKey -> callback

	stopC    chan struct{}
	stoppedC chan struct{}
	started  atomic.Bool
}

func (hw *sharedNamespaceWorker) run() {
	defer close(hw.stoppedC)

	hw.started.Store(true)

	ticker := time.NewTicker(hw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := hw.sendHeartbeats(); err != nil {
				hw.logger.Warn("Stopping heartbeat worker", "error", err)
				return
			}
		case <-hw.stopC:
			return
		}
	}
}

func (hw *sharedNamespaceWorker) sendHeartbeats() error {
	hw.mu.RLock()
	callbacks := make([]func() *workerpb.WorkerHeartbeat, 0, len(hw.callbacks))
	for _, cb := range hw.callbacks {
		callbacks = append(callbacks, cb)
	}
	hw.mu.RUnlock()

	if len(callbacks) == 0 {
		return nil
	}

	heartbeats := make([]*workerpb.WorkerHeartbeat, 0, len(callbacks))
	for _, cb := range callbacks {
		hb := cb()
		heartbeats = append(heartbeats, hb)
	}

	_, err := hw.client.recordWorkerHeartbeat(context.Background(), &workflowservice.RecordWorkerHeartbeatRequest{
		Namespace:       hw.namespace,
		WorkerHeartbeat: heartbeats,
	})

	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			// Server doesn't support heartbeats, shutdown worker
			hw.stop()
		}
		hw.logger.Warn("Failed to send heartbeat", "Error", err)
	}
	return err
}

func (hw *sharedNamespaceWorker) stop() {
	if !hw.started.CompareAndSwap(true, false) {
		return
	}

	close(hw.stopC)
	<-hw.stoppedC
}
