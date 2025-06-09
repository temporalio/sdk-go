package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

type eagerWorkerMock struct {
	releaseCalled            bool
	tryReserveSlotCallback   func() *SlotPermit
	processTaskAsyncCallback func(eagerTask)
}

func (e *eagerWorkerMock) tryReserveSlot() *SlotPermit {
	return e.tryReserveSlotCallback()
}

func (e *eagerWorkerMock) releaseSlot(_ *SlotPermit, _ SlotReleaseReason) {
	e.releaseCalled = true
}

func (e *eagerWorkerMock) pushEagerTask(task eagerTask) {
	e.processTaskAsyncCallback(task)
}

func TestEagerWorkflowDispatchNoWorkerOnTaskQueue(t *testing.T) {
	dispatcher := &eagerWorkflowDispatcher{
		workersByTaskQueue: make(map[string]map[eagerWorker]struct{}),
	}
	dispatcher.registerWorker(&workflowWorker{
		executionParameters: workerExecutionParameters{TaskQueue: "bad-task-queue"},
	})

	request := &workflowservice.StartWorkflowExecutionRequest{
		TaskQueue: &taskqueuepb.TaskQueue{Name: "task-queue"},
	}
	exec := dispatcher.applyToRequest(request)
	require.Nil(t, exec)
	require.False(t, request.GetRequestEagerExecution())
}

func TestEagerWorkflowDispatchAvailableWorker(t *testing.T) {
	dispatcher := &eagerWorkflowDispatcher{
		workersByTaskQueue: make(map[string]map[eagerWorker]struct{}),
	}

	availableWorker := &eagerWorkerMock{
		tryReserveSlotCallback: func() *SlotPermit { return &SlotPermit{} },
	}
	dispatcher.workersByTaskQueue["task-queue"] = map[eagerWorker]struct{}{
		&eagerWorkerMock{
			tryReserveSlotCallback: func() *SlotPermit { return nil },
		}: {},
		&eagerWorkerMock{
			tryReserveSlotCallback: func() *SlotPermit { return nil },
		}: {},
		availableWorker: {},
	}

	request := &workflowservice.StartWorkflowExecutionRequest{
		TaskQueue: &taskqueuepb.TaskQueue{Name: "task-queue"},
	}
	exec := dispatcher.applyToRequest(request)
	require.Equal(t, exec.worker, availableWorker)
	require.True(t, request.GetRequestEagerExecution())
}

func TestEagerWorkflowExecutor(t *testing.T) {
	processCalled := false
	permit := &SlotPermit{}
	worker := &eagerWorkerMock{
		tryReserveSlotCallback: nil, // isn't called in this situation
		processTaskAsyncCallback: func(task eagerTask) {
			require.Equal(t, permit, task.permit)
			processCalled = true
		},
	}

	exec := &eagerWorkflowExecutor{
		worker: worker,
		permit: permit,
	}
	exec.handleResponse(&workflowservice.PollWorkflowTaskQueueResponse{})
	require.True(t, processCalled)
	// Release will not have been called, since the real implementation would call it after
	// processing the task
	require.False(t, worker.releaseCalled)
	// This panics because we did use it - process was called
	require.Panics(t, func() {
		exec.releaseUnused()
	})
	// Panics because we already handled a response
	require.Panics(t, func() {
		exec.handleResponse(&workflowservice.PollWorkflowTaskQueueResponse{})
	})
}
