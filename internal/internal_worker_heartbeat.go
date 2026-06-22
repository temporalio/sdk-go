package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	workerservicepb "go.temporal.io/api/nexusservices/workerservice/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	ilog "go.temporal.io/sdk/internal/log"

	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// heartbeatManager manages heartbeat workers across namespaces for a client.
type heartbeatManager struct {
	client   *WorkflowClient
	interval time.Duration
	logger   log.Logger

	workersMutex sync.Mutex
	workers      map[string]*sharedNamespaceWorker // namespace -> worker
}

// newHeartbeatManager creates a new heartbeatManager.
func newHeartbeatManager(client *WorkflowClient, interval time.Duration, logger log.Logger) *heartbeatManager {
	if logger == nil {
		logger = ilog.NewDefaultLogger()
	}
	return &heartbeatManager{
		client:   client,
		interval: interval,
		logger:   logger,
		workers:  make(map[string]*sharedNamespaceWorker),
	}
}

func (m *heartbeatManager) sharedNamespaceWorkerFor(namespace string) *sharedNamespaceWorker {
	m.workersMutex.Lock()
	defer m.workersMutex.Unlock()
	return m.sharedNamespaceWorkerForLocked(namespace)
}

func (m *heartbeatManager) sharedNamespaceWorkerForLocked(namespace string) *sharedNamespaceWorker {
	hw := m.workers[namespace]
	if hw != nil {
		return hw
	}
	// If this is the first worker on the namespace, start a new shared namespace worker.
	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	controlTaskQueue := workerControlTaskQueue(namespace, m.client.workerGroupingKey)
	metricsHandler := m.client.metricsHandler
	if metricsHandler == nil {
		metricsHandler = metrics.NopHandler
	}
	hw = &sharedNamespaceWorker{
		client:                        m.client,
		namespace:                     namespace,
		interval:                      m.interval,
		workerCtx:                     heartbeatCtx,
		heartbeatCancel:               heartbeatCancel,
		callbacks:                     make(map[string]func() *workerpb.WorkerHeartbeat),
		activityCancellationCallbacks: newActivityCancellationCallbacks(),
		workerControlTaskQueue:        controlTaskQueue,
		workerInstanceKey:             uuid.NewString(),
		metricsHandler:                metricsHandler,
		stopC:                         make(chan struct{}),
		stoppedC:                      make(chan struct{}),
		logger:                        m.logger,
	}
	m.workers[namespace] = hw
	return hw
}

// registerWorker registers a worker's heartbeat callback with the shared heartbeat worker for the namespace.
func (m *heartbeatManager) registerWorker(
	worker *AggregatedWorker,
) error {
	nsData, err := m.client.loadNamespaceData(worker.heartbeatMetrics)
	if err != nil {
		return fmt.Errorf("failed to get namespace capabilities: %w", err)
	}
	if !nsData.capabilities.GetWorkerHeartbeats() {
		if m.logger != nil {
			m.logger.Debug("Worker heartbeating configured, but server version does not support it.")
		}
		return nil
	}

	namespace := worker.executionParams.Namespace
	m.workersMutex.Lock()
	defer m.workersMutex.Unlock()

	hw := m.sharedNamespaceWorkerForLocked(namespace)

	if hw.started.CompareAndSwap(false, true) {
		hw.workerCommandsSupported = nsData.capabilities.GetWorkerCommands()
		go hw.run()
	}

	hw.callbacksMutex.Lock()
	hw.callbacks[worker.workerInstanceKey] = worker.heartbeatCallback
	hw.callbacksMutex.Unlock()

	return nil
}

// unregisterWorker removes a worker's heartbeat callback. If no callbacks remain for the namespace,
// the shared heartbeat worker is stopped.
func (m *heartbeatManager) unregisterWorker(worker *AggregatedWorker) {
	m.workersMutex.Lock()
	defer m.workersMutex.Unlock()

	namespace := worker.executionParams.Namespace
	hw, ok := m.workers[namespace]
	if !ok {
		return
	}

	hw.callbacksMutex.Lock()
	delete(hw.callbacks, worker.workerInstanceKey)
	remaining := len(hw.callbacks)
	hw.callbacksMutex.Unlock()

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

	workerCtx       context.Context
	heartbeatCancel context.CancelFunc

	// callbacksMutex should only be unlocked under
	callbacksMutex sync.RWMutex
	callbacks      map[string]func() *workerpb.WorkerHeartbeat // workerInstanceKey -> callback

	activityCancellationCallbacks *activityCancellationCallbacks
	workerCommandsSupported       bool
	workerControlTaskQueue        string
	workerInstanceKey             string
	metricsHandler                metrics.Handler

	// stopC is created when the namespace heartbeat worker starts and closed by
	// sharedNamespaceWorker.stop() to tell run() to exit.
	stopC chan struct{}
	// stoppedC is created with stopC and closed by sharedNamespaceWorker.run()
	// after the heartbeat loop exits. stop() waits on it before returning.
	stoppedC chan struct{}
	started  atomic.Bool
}

func (hw *sharedNamespaceWorker) run() {
	defer close(hw.stoppedC)

	if hw.workerCommandsSupported {
		workerCommandsDone := make(chan struct{})
		go func() {
			defer close(workerCommandsDone)
			hw.runWorkerCommands()
		}()
		defer func() {
			hw.heartbeatCancel()
			<-workerCommandsDone
		}()
	}

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
	hw.callbacksMutex.RLock()
	callbacks := make([]func() *workerpb.WorkerHeartbeat, 0, len(hw.callbacks))
	for _, cb := range hw.callbacks {
		if cb != nil {
			callbacks = append(callbacks, cb)
		}
	}
	hw.callbacksMutex.RUnlock()

	if len(callbacks) == 0 {
		return nil
	}

	heartbeats := make([]*workerpb.WorkerHeartbeat, 0, len(callbacks))
	for _, cb := range callbacks {
		hb := cb()
		heartbeats = append(heartbeats, hb)
	}

	_, err := hw.client.recordWorkerHeartbeat(hw.workerCtx, &workflowservice.RecordWorkerHeartbeatRequest{
		Namespace:       hw.namespace,
		WorkerHeartbeat: heartbeats,
	})

	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			// Server doesn't support heartbeats; return error to stop the worker.
			return fmt.Errorf("server does not support worker heartbeats: %w", err)
		}
		// For other errors, log and continue heartbeating
		hw.logger.Warn("Failed to send heartbeat", "Error", err)
	}
	return nil
}

func (hw *sharedNamespaceWorker) runWorkerCommands() {
	for {
		select {
		case <-hw.workerCtx.Done():
			return
		default:
		}

		task, err := hw.pollWorkerCommandTask()
		if err != nil {
			if hw.workerCtx.Err() != nil {
				return
			}
			hw.logger.Warn("Failed polling worker command task", "Error", err)
			select {
			case <-time.After(time.Second):
			case <-hw.workerCtx.Done():
				return
			}
			continue
		}
		if task == nil || len(task.TaskToken) == 0 {
			continue
		}
		if task.GetRequest() == nil {
			hw.logger.Warn("Received worker command task with nil request")
			continue
		}

		if err := hw.handleWorkerCommandTask(task); err != nil {
			hw.logger.Warn("Failed handling worker command task", "Error", err)
		}
	}
}

func (hw *sharedNamespaceWorker) pollWorkerCommandTask() (*workflowservice.PollNexusTaskQueueResponse, error) {
	rpcMetricsHandler := hw.metricsHandler.WithTags(metrics.TaskQueueTags(hw.workerControlTaskQueue))
	grpcCtx, cancel := newGRPCContext(
		hw.workerCtx,
		grpcMetricsHandler(rpcMetricsHandler),
		grpcLongPoll(true),
		grpcTimeout(pollTaskServiceTimeOut),
		defaultGrpcRetryParameters(hw.workerCtx),
	)
	defer cancel()

	return hw.client.workflowService.PollNexusTaskQueue(grpcCtx, &workflowservice.PollNexusTaskQueueRequest{
		Namespace: hw.namespace,
		TaskQueue: &taskqueuepb.TaskQueue{
			Name: hw.workerControlTaskQueue,
			Kind: enumspb.TASK_QUEUE_KIND_WORKER_COMMANDS,
		},
		Identity:          hw.client.identity,
		WorkerInstanceKey: hw.workerInstanceKey,
		WorkerVersionCapabilities: &commonpb.WorkerVersionCapabilities{
			BuildId: "1.0",
		},
	})
}

func (hw *sharedNamespaceWorker) handleWorkerCommandTask(task *workflowservice.PollNexusTaskQueueResponse) error {
	req := task.GetRequest()
	startOp := req.GetStartOperation()
	if startOp == nil {
		return fmt.Errorf("worker command nexus task has unexpected request variant")
	}

	var execReq workerservicepb.ExecuteCommandsRequest
	if err := proto.Unmarshal(startOp.GetPayload().GetData(), &execReq); err != nil {
		return fmt.Errorf("decoding ExecuteCommandsRequest: %w", err)
	}

	results := make([]*workerpb.WorkerCommandResult, 0, len(execReq.GetCommands()))
	for _, command := range execReq.GetCommands() {
		switch cmd := command.GetType().(type) {
		case *workerpb.WorkerCommand_CancelActivity:
			if cmd.CancelActivity != nil && hw.activityCancellationCallbacks != nil {
				hw.activityCancellationCallbacks.cancel(cmd.CancelActivity.GetTaskToken())
			}
			results = append(results, &workerpb.WorkerCommandResult{
				Type: &workerpb.WorkerCommandResult_CancelActivity{
					CancelActivity: &workerpb.CancelActivityResult{},
				},
			})
		default:
			hw.logger.Warn("Worker command has unsupported command type")
			results = append(results, &workerpb.WorkerCommandResult{})
		}
	}

	responsePayload, err := proto.Marshal(&workerservicepb.ExecuteCommandsResponse{Results: results})
	if err != nil {
		return fmt.Errorf("encode ExecuteCommandsResponse: %w", err)
	}

	grpcCtx, cancel := newGRPCContext(
		context.Background(),
		grpcMetricsHandler(hw.metricsHandler),
		defaultGrpcRetryParameters(context.Background()),
	)
	defer cancel()
	_, err = hw.client.workflowService.RespondNexusTaskCompleted(grpcCtx, &workflowservice.RespondNexusTaskCompletedRequest{
		Namespace:     hw.namespace,
		Identity:      hw.client.identity,
		TaskToken:     task.TaskToken,
		PollerGroupId: task.PollerGroupId,
		Response: &nexuspb.Response{
			Variant: &nexuspb.Response_StartOperation{
				StartOperation: &nexuspb.StartOperationResponse{
					Variant: &nexuspb.StartOperationResponse_SyncSuccess{
						SyncSuccess: &nexuspb.StartOperationResponse_Sync{
							Payload: &commonpb.Payload{Data: responsePayload},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("complete worker command nexus task: %w", err)
	}
	return nil
}

func (hw *sharedNamespaceWorker) stop() {
	if !hw.started.CompareAndSwap(true, false) {
		return
	}
	if hw.heartbeatCancel != nil {
		hw.heartbeatCancel()
	}

	close(hw.stopC)
	<-hw.stoppedC
}

// pollTimeTracker tracks the last successful poll time for each poller type.
type pollTimeTracker struct {
	times sync.Map // pollerType (string) -> time.Time (stored as int64 nanos)
}

func (p *pollTimeTracker) recordPollSuccess(pollerType string) {
	p.times.Store(pollerType, time.Now().UnixNano())
}

func (p *pollTimeTracker) getLastPollTime(pollerType string) time.Time {
	if v, ok := p.times.Load(pollerType); ok {
		return time.Unix(0, v.(int64))
	}
	return time.Time{}
}
