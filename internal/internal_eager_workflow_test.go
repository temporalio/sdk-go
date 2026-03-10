package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

type eagerWorkerMock struct {
	releaseCalled            bool
	tryReserveSlotCallback   func() *SlotPermit
	processTaskAsyncCallback func(eagerTask)
	deploymentOptions        WorkerDeploymentOptions
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

func (e *eagerWorkerMock) getDeploymentOptions() WorkerDeploymentOptions {
	return e.deploymentOptions
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

func TestEagerWorkflowDispatchWithDeploymentOptions(t *testing.T) {
	dispatcher := &eagerWorkflowDispatcher{
		workersByTaskQueue: make(map[string]map[eagerWorker]struct{}),
	}

	deploymentVersion := WorkerDeploymentVersion{
		DeploymentName: "test-deployment",
		BuildID:        "test-build-id",
	}

	workerWithDeployment := &eagerWorkerMock{
		tryReserveSlotCallback: func() *SlotPermit { return &SlotPermit{} },
		deploymentOptions: WorkerDeploymentOptions{
			UseVersioning: true,
			Version:       deploymentVersion,
		},
	}
	dispatcher.workersByTaskQueue["task-queue"] = map[eagerWorker]struct{}{
		workerWithDeployment: {},
	}

	request := &workflowservice.StartWorkflowExecutionRequest{
		TaskQueue: &taskqueuepb.TaskQueue{Name: "task-queue"},
	}
	exec := dispatcher.applyToRequest(request)

	require.NotNil(t, exec)
	require.Equal(t, workerWithDeployment, exec.worker)
	require.True(t, request.GetRequestEagerExecution())

	require.NotNil(t, request.EagerWorkerDeploymentOptions)
	ewdo := request.EagerWorkerDeploymentOptions
	require.Equal(t, "test-deployment", ewdo.DeploymentName)
	require.Equal(t, "test-build-id", ewdo.BuildId)
	require.Equal(t, enums.WORKER_VERSIONING_MODE_VERSIONED, ewdo.WorkerVersioningMode)
}

func TestEagerWorkflowDispatchWithoutDeploymentVersioning(t *testing.T) {
	dispatcher := &eagerWorkflowDispatcher{
		workersByTaskQueue: make(map[string]map[eagerWorker]struct{}),
	}

	// Worker without deployment versioning enabled
	workerWithoutVersioning := &eagerWorkerMock{
		tryReserveSlotCallback: func() *SlotPermit { return &SlotPermit{} },
		deploymentOptions: WorkerDeploymentOptions{
			UseVersioning: false,
			Version: WorkerDeploymentVersion{
				DeploymentName: "test-deployment",
				BuildID:        "test-build-id",
			},
		},
	}
	dispatcher.workersByTaskQueue["task-queue"] = map[eagerWorker]struct{}{
		workerWithoutVersioning: {},
	}

	request := &workflowservice.StartWorkflowExecutionRequest{
		TaskQueue: &taskqueuepb.TaskQueue{Name: "task-queue"},
	}
	exec := dispatcher.applyToRequest(request)

	require.NotNil(t, exec)
	require.True(t, request.GetRequestEagerExecution())
	require.NotNil(t, request.EagerWorkerDeploymentOptions)
	ewdo := request.EagerWorkerDeploymentOptions
	require.Equal(t, "test-deployment", ewdo.DeploymentName)
	require.Equal(t, "test-build-id", ewdo.BuildId)
	require.Equal(t, enums.WORKER_VERSIONING_MODE_UNVERSIONED, ewdo.WorkerVersioningMode)
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
