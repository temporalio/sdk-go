package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
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
	defer m.mu.Unlock()

	namespace := worker.executionParams.Namespace
	hw, ok := m.workers[namespace]
	if !ok {
		capabilities, err := m.client.loadNamespaceCapabilities(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get namespace capabilities: %w", err)
		}
		if !capabilities.WorkerHeartbeats {
			m.logger.Debug("Worker heartbeating configured, but server version does not support it.")
			return nil
		}

		hw = &sharedNamespaceWorker{
			client:    m.client,
			namespace: namespace,
			taskQueue: fmt.Sprintf("temporal-sys/worker-commands/%s/%s", namespace, m.client.workerGroupingKey),
			interval:  m.interval,
			callbacks: make(map[string]func() *workerpb.WorkerHeartbeat),
			stopC:     make(chan struct{}),
			stoppedC:  make(chan struct{}),
			logger:    m.logger,
		}

		nexusWorker, err := hw.createNexusWorker()
		if err != nil {
			return fmt.Errorf("failed to create nexus worker for heartbeating: %w", err)
		}
		hw.nexusWorker = nexusWorker

		m.workers[namespace] = hw
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

	reg := nexus.NewServiceRegistry()
	handler, err := reg.NewHandler()
	if err != nil {
		return nil, err
	}

	// TODO: Register worker commands here

	nw, err := newNexusWorker(nexusWorkerOptions{
		executionParameters: params,
		client:              hw.client,
		workflowService:     hw.client.workflowService,
		handler:             handler,
	})

	return nw, err
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

	_, err := hw.client.RecordWorkerHeartbeat(context.Background(), &workflowservice.RecordWorkerHeartbeatRequest{
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

func (hw *sharedNamespaceWorker) stop() {
	if !hw.started.CompareAndSwap(true, false) {
		return
	}

	close(hw.stopC)
	<-hw.stoppedC
}
