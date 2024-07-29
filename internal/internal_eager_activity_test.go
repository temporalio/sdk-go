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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func TestEagerActivityDisabled(t *testing.T) {
	exec := newEagerActivityExecutor(eagerActivityExecutorOptions{disabled: true, taskQueue: "task-queue1"})
	exec.activityWorker = newActivityWorker(nil,
		workerExecutionParameters{TaskQueue: "task-queue1"}, nil, newRegistry(), nil).worker

	// Turns requests to false when disabled
	var req workflowservice.RespondWorkflowTaskCompletedRequest
	addScheduleTaskCommand(&req, "task-queue1")
	require.Empty(t, exec.applyToRequest(&req))
	require.False(t, req.Commands[0].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
}

func TestEagerActivityNoActivityWorker(t *testing.T) {
	exec := newEagerActivityExecutor(eagerActivityExecutorOptions{taskQueue: "task-queue1"})

	// Turns requests to false without activity worker
	var req workflowservice.RespondWorkflowTaskCompletedRequest
	addScheduleTaskCommand(&req, "task-queue1")
	require.Empty(t, exec.applyToRequest(&req))
	require.False(t, req.Commands[0].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
}

func TestEagerActivityWrongTaskQueue(t *testing.T) {
	exec := newEagerActivityExecutor(eagerActivityExecutorOptions{taskQueue: "task-queue1"})
	activityWorker := newActivityWorker(nil,
		workerExecutionParameters{TaskQueue: "task-queue1",
			Tuner: NewFixedSizeTuner(FixedSizeTunerOptions{
				NumWorkflowSlots:      defaultMaxConcurrentTaskExecutionSize,
				NumActivitySlots:      10,
				NumLocalActivitySlots: defaultMaxConcurrentLocalActivityExecutionSize})},
		nil, newRegistry(), nil)
	activityWorker.worker.isWorkerStarted = true

	exec.activityWorker = activityWorker.worker

	// Turns requests to false when wrong task queue
	var req workflowservice.RespondWorkflowTaskCompletedRequest
	addScheduleTaskCommand(&req, "task-queue1")
	addScheduleTaskCommand(&req, "task-queue2")
	require.Equal(t, 1, len(exec.applyToRequest(&req)))
	require.True(t, req.Commands[0].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.False(t, req.Commands[1].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
}

func TestEagerActivityMaxPerTask(t *testing.T) {
	exec := newEagerActivityExecutor(eagerActivityExecutorOptions{taskQueue: "task-queue1"})
	activityWorker := newActivityWorker(nil,
		workerExecutionParameters{TaskQueue: "task-queue1",
			Tuner: NewFixedSizeTuner(FixedSizeTunerOptions{
				NumWorkflowSlots:      defaultMaxConcurrentTaskExecutionSize,
				NumActivitySlots:      10,
				NumLocalActivitySlots: defaultMaxConcurrentLocalActivityExecutionSize})},
		nil, newRegistry(), nil)
	activityWorker.worker.isWorkerStarted = true

	exec.activityWorker = activityWorker.worker

	// Add 8, but it limits to only the first 3
	var req workflowservice.RespondWorkflowTaskCompletedRequest
	for i := 0; i < 8; i++ {
		addScheduleTaskCommand(&req, "task-queue1")
	}
	require.Equal(t, 3, len(exec.applyToRequest(&req)))
	for i := 0; i < 8; i++ {
		require.Equal(t, i < 3, req.Commands[i].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	}
}

func TestEagerActivityCounts(t *testing.T) {
	// We'll create an eager activity executor with 3 max eager concurrent and 5
	// max concurrent
	exec := newEagerActivityExecutor(eagerActivityExecutorOptions{taskQueue: "task-queue1", maxConcurrent: 3})
	tuner := NewFixedSizeTuner(FixedSizeTunerOptions{
		NumWorkflowSlots:      defaultMaxConcurrentTaskExecutionSize,
		NumActivitySlots:      5,
		NumLocalActivitySlots: defaultMaxConcurrentLocalActivityExecutionSize})
	activityWorker := newActivityWorker(nil,
		workerExecutionParameters{TaskQueue: "task-queue1", Tuner: tuner}, nil, newRegistry(), nil)
	activityWorker.worker.isWorkerStarted = true
	go activityWorker.worker.runEagerTaskDispatcher()

	exec.activityWorker = activityWorker.worker
	// Replace task processor
	taskProcessor := newWaitingTaskProcessor()
	activityWorker.worker.options.taskWorker = taskProcessor

	// Request 2 commands on wrong task queue then 5 commands on proper task queue
	// but have 2nd request disabled
	req := &workflowservice.RespondWorkflowTaskCompletedRequest{}
	addScheduleTaskCommand(req, "task-queue2")
	addScheduleTaskCommand(req, "task-queue2")
	addScheduleTaskCommand(req, "task-queue1")
	addScheduleTaskCommand(req, "task-queue1").RequestEagerExecution = false
	addScheduleTaskCommand(req, "task-queue1")
	addScheduleTaskCommand(req, "task-queue1")
	addScheduleTaskCommand(req, "task-queue1")

	// Apply to request and confirm only the proper 3 remain as true
	require.Equal(t, 3, len(exec.applyToRequest(req)))
	require.False(t, req.Commands[0].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.False(t, req.Commands[1].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.True(t, req.Commands[2].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.False(t, req.Commands[3].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.True(t, req.Commands[4].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.True(t, req.Commands[5].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.False(t, req.Commands[6].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)

	// Confirm counts
	tss := activityWorker.worker.slotSupplier
	require.Equal(t, int32(3), tss.issuedSlotsAtomic.Load())
	// None are used at this point
	require.Equal(t, 0, len(tss.usedSlots))

	// Pretend server only returned 2 eager activities
	resp := &workflowservice.RespondWorkflowTaskCompletedResponse{
		ActivityTasks: []*workflowservice.PollActivityTaskQueueResponse{
			{ActivityId: "activity1"},
			{ActivityId: "activity2"},
		},
	}
	exec.handleResponse(resp, []*SlotPermit{{}, {}, {}})

	// Wait a bit until both tasks running
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&taskProcessor.numWaiting) == 2
	}, 2*time.Second, 100*time.Millisecond)

	// Confirm counts
	require.Equal(t, int32(2), tss.issuedSlotsAtomic.Load())
	// Both are used
	require.Equal(t, 2, len(tss.usedSlots))

	// Try a request with two more eager and confirm only room for one
	req = &workflowservice.RespondWorkflowTaskCompletedRequest{}
	addScheduleTaskCommand(req, "task-queue1")
	addScheduleTaskCommand(req, "task-queue1")
	require.Equal(t, 1, len(exec.applyToRequest(req)))
	require.True(t, req.Commands[0].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.False(t, req.Commands[1].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.Equal(t, int32(3), tss.issuedSlotsAtomic.Load())

	// Resolve that saying none came back
	exec.handleResponse(nil, []*SlotPermit{{}})
	require.Equal(t, int32(2), tss.issuedSlotsAtomic.Load())

	// Now take all remaining slots from the activity side and confirm we can't
	// reserve any eager
	for {
		permit := tss.TryReserveSlot(&slotReservationData{taskQueue: "task-queue1"})
		if permit == nil {
			break
		}
	}

	req = &workflowservice.RespondWorkflowTaskCompletedRequest{}
	addScheduleTaskCommand(req, "task-queue1")
	require.Empty(t, exec.applyToRequest(req))
	require.False(t, req.Commands[0].GetScheduleActivityTaskCommandAttributes().RequestEagerExecution)
	require.Equal(t, int32(5), tss.issuedSlotsAtomic.Load())

	// Complete eager two and confirm those are released. The three we took by hand from the
	// slot supplier won't be released since no one but this test knows about them.
	taskProcessor.completeCh <- struct{}{}
	taskProcessor.completeCh <- struct{}{}
	require.Eventually(t, func() bool {
		return int32(3) == tss.issuedSlotsAtomic.Load()
	}, 2*time.Second, 100*time.Millisecond)
}

func addScheduleTaskCommand(
	req *workflowservice.RespondWorkflowTaskCompletedRequest,
	taskQueue string,
) *commandpb.ScheduleActivityTaskCommandAttributes {
	ret := &commandpb.ScheduleActivityTaskCommandAttributes{
		RequestEagerExecution: true,
		TaskQueue:             &taskqueuepb.TaskQueue{Name: taskQueue},
	}
	req.Commands = append(req.Commands, &commandpb.Command{
		CommandType: enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK,
		Attributes: &commandpb.Command_ScheduleActivityTaskCommandAttributes{
			ScheduleActivityTaskCommandAttributes: ret,
		},
	})
	return ret
}

type waitingTaskProcessor struct {
	numWaiting int32
	completeCh chan struct{}
}

func newWaitingTaskProcessor() *waitingTaskProcessor {
	return &waitingTaskProcessor{completeCh: make(chan struct{})}
}

func (*waitingTaskProcessor) PollTask() (taskForWorker, error) {
	return nil, fmt.Errorf("not implemented")
}

func (w *waitingTaskProcessor) ProcessTask(interface{}) error {
	atomic.AddInt32(&w.numWaiting, 1)
	defer atomic.AddInt32(&w.numWaiting, -1)
	<-w.completeCh
	return nil
}
