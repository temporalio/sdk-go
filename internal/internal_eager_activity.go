package internal

import (
	"fmt"
	"sync"

	"go.temporal.io/api/workflowservice/v1"
)

// eagerActivityExecutor is a worker-scoped executor for eager activities that
// are returned from workflow task completion responses.
type eagerActivityExecutor struct {
	eagerActivityExecutorOptions

	activityWorker *activityWorker
	pendingCount   int // Only access under countLock
	executingCount int // Only access under countLock
	countLock      sync.Mutex
}

type eagerActivityExecutorOptions struct {
	disabled  bool
	taskQueue string
	// If 0, there is no maximum
	maxConcurrent int
}

// newEagerActivityExecutor creates a new worker-scoped executor without an
// activityWorker set. The activityWorker must be set on the responding executor
// before it will be able to execute activities.
func newEagerActivityExecutor(options eagerActivityExecutorOptions) *eagerActivityExecutor {
	return &eagerActivityExecutor{eagerActivityExecutorOptions: options}
}

// newWorkflowEagerActivityExecutor creates a new workflow-scoped executor. This
// should be used by individual workflows to request and execute eager
// activities.
func (e *eagerActivityExecutor) newWorkflowEagerActivityExecutor() *workflowEagerActivityExecutor {
	if e == nil {
		return nil
	}
	return &workflowEagerActivityExecutor{eagerActivityExecutor: e}
}

// workflowEagerActivityExecutor is a workflow-scoped executor that allows
// workflows to request/execute eager activities. None of the calls here are
// safe for execution from multiple goroutines.
type workflowEagerActivityExecutor struct {
	*eagerActivityExecutor
	pendingCountThisTask int
}

// acquire requests and then reserves an eager activity slot. If a slot cannot
// be reserved for whatever reason, false is returned. Callers are expected to
// call as many requests as are needed for a single task. Then callers must
// call releaseAndExecute with the response even if there are no activities to
// execute.
func (w *workflowEagerActivityExecutor) acquire(options *ExecuteActivityOptions) bool {
	// Must be present, have an activity worker, be enabled at worker level, be
	// enabled at activity level, and be on same task queue
	if w == nil || w.activityWorker == nil || w.disabled || options.DisableEagerExecution || options.TaskQueueName != w.taskQueue {
		return false
	}

	// We do the rest under worker-wide lock. Technically we could receive from
	// the poller request channel before lock and then check counts later, but the
	// receive is non-blocking anyways and we don't have to make sure to put it
	// back on over-count like we would if we did it first.
	w.countLock.Lock()
	defer w.countLock.Unlock()

	// Confirm that, if we have a max, pending + executing isn't already there
	if w.maxConcurrent > 0 && w.executingCount+w.pendingCount >= w.maxConcurrent {
		// No more room
		return false
	}

	// Reserve a spot for our request via a non-blocking attempt to take a poller
	// request entry which essentially reserves a spot
	select {
	case <-w.activityWorker.worker.pollerRequestCh:
	default:
		return false
	}

	// We can request, so increase the pending and local pending counts
	w.pendingCount++
	w.pendingCountThisTask++
	return true
}

// releaseAndExecute releases all reserved slots from acquire and starts all of
// the eager activities in non-blocking fashion starts all of the eager
// activities. This call must be made after any number of acquire calls to
// reclaim reserved slots.
func (w *workflowEagerActivityExecutor) releaseAndExecute(activities []*workflowservice.PollActivityTaskQueueResponse) {
	// Ignore disabled
	if w == nil || w.activityWorker == nil || w.disabled {
		return
	} else if len(activities) > w.pendingCountThisTask {
		panic(fmt.Sprintf("Unexpectedly received %v eager activities though we only requested %v",
			len(activities), w.pendingCountThisTask))
	}

	// Update counts under lock
	w.countLock.Lock()
	// Record the number of unfulfilled slots we have to give back
	unfulfilledSlots := w.pendingCountThisTask - len(activities)
	// Remove all pending from worker-scope pending
	w.pendingCount -= w.pendingCountThisTask
	// Add the activity count to the executing count
	w.executingCount += len(activities)
	// Reset the workflow pending count (technically no lock needed but meh)
	w.pendingCountThisTask = 0
	w.countLock.Unlock()

	// Put every unfulfilled slot back on the poller channel
	for i := 0; i < unfulfilledSlots; i++ {
		// Like other parts that push onto this channel, we assume there is room
		// because we took it, so we do a blocking send
		w.activityWorker.worker.pollerRequestCh <- struct{}{}
	}

	// Start each activity asynchronously
	for _, activity := range activities {
		// Before starting the goroutine we have to increase the wait group counter
		// that the poller would have otherwise increased
		w.activityWorker.worker.stopWG.Add(1)
		// Asynchronously execute
		task := &activityTask{activity}
		go func() {
			// Like other sends to this channel, we assume there is room because we
			// reserved it, so we make a blocking send. The processTask does not do
			// this itself because our task is *activityTask, not *polledTask.
			w.activityWorker.worker.pollerRequestCh <- struct{}{}
			// Decrement executing count
			w.countLock.Lock()
			w.executingCount--
			w.countLock.Unlock()

			// Process the task synchronously. We call the processor on the base
			// worker instead of a higher level so we can get the benefits of metrics,
			// stop wait group update, etc.
			w.activityWorker.worker.processTask(task)
		}()
	}
}
