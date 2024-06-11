// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
	"encoding/binary"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	historypb "go.temporal.io/api/history/v1"
	protocolpb "go.temporal.io/api/protocol/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/update/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

type countingTaskHandler struct {
	WorkflowTaskHandler
	ProcessWorkflowTaskInvocationCount atomic.Uint32
}

func (wth *countingTaskHandler) ProcessWorkflowTask(
	task *workflowTask,
	wfctx *workflowExecutionContextImpl,
	hb workflowTaskHeartbeatFunc,
) (interface{}, error) {
	wth.ProcessWorkflowTaskInvocationCount.Add(1)
	return wth.WorkflowTaskHandler.ProcessWorkflowTask(task, wfctx, hb)
}

func TestWFTRacePrevention(t *testing.T) {
	params := workerExecutionParameters{cache: NewWorkerCache()}
	ensureRequiredParams(&params)
	var (
		taskQueue    = taskqueuepb.TaskQueue{Name: t.Name() + "task-queue"}
		startedAttrs = historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskQueue,
		}
		startedEvent     = createTestEventWorkflowExecutionStarted(1, &startedAttrs)
		history          = historypb.History{Events: []*historypb.HistoryEvent{startedEvent}}
		runID            = t.Name() + "-run-id"
		wfID             = t.Name() + "-workflow-id"
		wfe              = commonpb.WorkflowExecution{RunId: runID, WorkflowId: wfID}
		wfType           = commonpb.WorkflowType{Name: t.Name() + "-workflow-type"}
		ctrl             = gomock.NewController(t)
		client           = workflowservicemock.NewMockWorkflowServiceClient(ctrl)
		resultsChan      = make(chan error, 2)
		innerTaskHandler = newWorkflowTaskHandler(params, nil, newRegistry())
		taskHandler      = &countingTaskHandler{WorkflowTaskHandler: innerTaskHandler}
		contextManager   = taskHandler
		codec            = binary.LittleEndian
		completionChans  = []chan struct{}{make(chan struct{}), make(chan struct{})}
		pollResp0        = workflowservice.PollWorkflowTaskQueueResponse{
			Attempt:           1,
			WorkflowExecution: &wfe,
			WorkflowType:      &wfType,
			History:           &history,
			// encode the task pseudo-ID into the token; 0 here and 1 for
			// pollResp1 below. The mock will use this as an index into
			// `completionChans` (above) to get a task-specific control channel.
			TaskToken: codec.AppendUint32(nil, 0),
		}
		pollResp1 = workflowservice.PollWorkflowTaskQueueResponse{
			Attempt:           1,
			WorkflowExecution: &wfe,
			WorkflowType:      &wfType,
			History:           &history,
			TaskToken:         codec.AppendUint32(nil, 1),
		}
		task0 = workflowTask{task: &pollResp0}
		task1 = workflowTask{task: &pollResp1}
	)

	t.Log("Didn't register any workflows so expect both future WFTs to " +
		"end up calling RespondWorkflowTaskFailed")
	client.EXPECT().RespondWorkflowTaskFailed(gomock.Any(), gomock.Any()).
		Times(2).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.RespondWorkflowTaskFailedRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.RespondWorkflowTaskFailedResponse, error) {
			// find the appropriate channel for this task - the index is encoded
			// into the TaskToken
			ch := completionChans[int(codec.Uint32(req.TaskToken))]
			<-ch
			// these two reads ^v allow the test code to capture a task processing
			// goroutine exactly here
			<-ch
			return &workflowservice.RespondWorkflowTaskFailedResponse{}, nil
		})

	poller := newWorkflowTaskPoller(taskHandler, contextManager, client, params)

	t.Log("Issue task0")
	go func() { resultsChan <- poller.processWorkflowTask(&task0) }()

	completionChans[0] <- struct{}{}
	require.EqualValues(t, 1, taskHandler.ProcessWorkflowTaskInvocationCount.Load(),
		"TaskHandler.ProcessWorkflowTask should have been called once")
	t.Log("task0 has called TaskHandler.ProcessWorkflowTask and is blocked " +
		"in the mock RespondWorkflowTaskFailed")

	t.Log("Issue task1")
	go func() { resultsChan <- poller.processWorkflowTask(&task1) }()

	require.EqualValues(t, 1, taskHandler.ProcessWorkflowTaskInvocationCount.Load(),
		"TaskHandler.ProcessWorkflowTask should only have been called once")

	t.Log("Unblock task0 allowing poller.processWorkflowTask to return")
	close(completionChans[0])
	require.NoError(t, <-resultsChan)

	t.Log("task1 should now proceed and block in the mock RespondWorkflowTaskFailed")
	completionChans[1] <- struct{}{}
	require.EqualValues(t, 2, taskHandler.ProcessWorkflowTaskInvocationCount.Load(),
		"TaskHandler.ProcessWorkflowTask should have been called twice")

	t.Log("Unblock task1 allowing poller.processWorkflowTask to return")
	close(completionChans[1])
	require.NoError(t, <-resultsChan)
}

func TestWFTCorruption(t *testing.T) {
	cache := NewWorkerCache()
	params := workerExecutionParameters{cache: cache}
	ensureRequiredParams(&params)
	wfType := commonpb.WorkflowType{Name: t.Name() + "-workflow-type"}
	reg := newRegistry()
	reg.RegisterWorkflowWithOptions(func(ctx Context) error {
		return Await(ctx, func() bool {
			return false
		})
	}, RegisterWorkflowOptions{
		Name: wfType.Name,
	})
	var (
		taskQueue    = taskqueuepb.TaskQueue{Name: t.Name() + "task-queue"}
		startedAttrs = historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskQueue,
		}
		startedEvent     = createTestEventWorkflowExecutionStarted(1, &startedAttrs)
		history          = historypb.History{Events: []*historypb.HistoryEvent{startedEvent}}
		runID            = t.Name() + "-run-id"
		wfID             = t.Name() + "-workflow-id"
		wfe              = commonpb.WorkflowExecution{RunId: runID, WorkflowId: wfID}
		ctrl             = gomock.NewController(t)
		client           = workflowservicemock.NewMockWorkflowServiceClient(ctrl)
		innerTaskHandler = newWorkflowTaskHandler(params, nil, reg)
		taskHandler      = &countingTaskHandler{WorkflowTaskHandler: innerTaskHandler}
		contextManager   = taskHandler
		completionChans  = []chan struct{}{make(chan struct{}), make(chan struct{})}
		codec            = binary.LittleEndian
		pollResp0        = workflowservice.PollWorkflowTaskQueueResponse{
			Attempt:           1,
			WorkflowExecution: &wfe,
			WorkflowType:      &wfType,
			History:           &history,
			// encode the task pseudo-ID into the token; 0 here and 1 for
			// pollResp1 below. The mock will use this as an index into
			// `completionChans` (above) to get a task-specific control channel.
			TaskToken: codec.AppendUint32(nil, 0),
		}
		task0 = workflowTask{task: &pollResp0}
	)

	// Return an error on respond workflow task complete, the SDK should flush the workflow from cache
	client.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(
			_ context.Context,
			req *workflowservice.RespondWorkflowTaskCompletedRequest,
			_ ...grpc.CallOption,
		) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			// find the appropriate channel for this task - the index is encoded
			// into the TaskToken
			ch := completionChans[int(codec.Uint32(req.TaskToken))]
			<-ch
			// these two reads ^v allow the test code to capture a task processing
			// goroutine exactly here
			<-ch
			return nil, errors.New("Failure responding to workflow task")
		})

	poller := newWorkflowTaskPoller(taskHandler, contextManager, client, params)
	processTaskDone := make(chan struct{})
	go func() {
		require.Error(t, poller.processWorkflowTask(&task0))
		close(processTaskDone)
	}()
	completionChans[0] <- struct{}{}
	// Until RespondWorkflowTaskCompleted returns an error the workflow should be in cache
	require.True(t, (*cache.sharedCache.workflowCache).Exist(runID))
	close(completionChans[0])
	<-processTaskDone
	// Workflow should not be in cache
	require.Nil(t, cache.getWorkflowContext(runID))
}

func TestWFTReset(t *testing.T) {
	cache := NewWorkerCache()
	params := workerExecutionParameters{
		cache: cache,
	}
	ensureRequiredParams(&params)
	wfType := commonpb.WorkflowType{Name: t.Name() + "-workflow-type"}
	reg := newRegistry()
	reg.RegisterWorkflowWithOptions(func(ctx Context) error {
		_ = SetUpdateHandler(ctx, "update", func(ctx Context) error {
			return nil
		}, UpdateHandlerOptions{
			Validator: func(ctx Context) error {
				return errors.New("rejecting for test")
			},
		})
		_ = Sleep(ctx, time.Second)
		return Sleep(ctx, time.Second)
	}, RegisterWorkflowOptions{
		Name: wfType.Name,
	})
	var (
		taskQueue = taskqueuepb.TaskQueue{Name: t.Name() + "task-queue"}
		history0  = historypb.History{Events: []*historypb.HistoryEvent{
			createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
				TaskQueue: &taskQueue,
			}),
			createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{
				TaskQueue:           &taskQueue,
				StartToCloseTimeout: &durationpb.Duration{Seconds: 10},
				Attempt:             1,
			}),
			createTestEventWorkflowTaskStarted(3),
			createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
				ScheduledEventId: 2,
				StartedEventId:   3,
			}),
			createTestEventTimerStarted(5, 5),
			createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{
				TaskQueue:           &taskQueue,
				StartToCloseTimeout: &durationpb.Duration{Seconds: 10},
				Attempt:             1,
			}),
			createTestEventWorkflowTaskStarted(7),
		}}
		messages = []*protocolpb.Message{
			createTestProtocolMessageUpdateRequest("test-update", 6, &update.Request{
				Meta: &update.Meta{
					UpdateId: "test-update",
				},
				Input: &update.Input{
					Name: "update",
				},
			}),
		}
		history1 = historypb.History{Events: []*historypb.HistoryEvent{
			createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
				ScheduledEventId: 2,
				StartedEventId:   3,
			}),
			createTestEventTimerStarted(5, 5),
			createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{
				TaskQueue:           &taskQueue,
				StartToCloseTimeout: &durationpb.Duration{Seconds: 10},
				Attempt:             1,
			}),
			createTestEventWorkflowTaskStarted(7),
		}}
		history2 = historypb.History{Events: []*historypb.HistoryEvent{
			createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{
				ScheduledEventId: 2,
				StartedEventId:   3,
			}),
			createTestEventTimerStarted(5, 5),
			createTestEventTimerFired(6, 5),
			createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{
				TaskQueue:           &taskQueue,
				StartToCloseTimeout: &durationpb.Duration{Seconds: 10},
				Attempt:             1,
			}),
			createTestEventWorkflowTaskStarted(8),
		}}
		runID            = t.Name() + "-run-id"
		wfID             = t.Name() + "-workflow-id"
		wfe              = commonpb.WorkflowExecution{RunId: runID, WorkflowId: wfID}
		ctrl             = gomock.NewController(t)
		client           = workflowservicemock.NewMockWorkflowServiceClient(ctrl)
		innerTaskHandler = newWorkflowTaskHandler(params, nil, reg)
		taskHandler      = &countingTaskHandler{WorkflowTaskHandler: innerTaskHandler}
		contextManager   = taskHandler
		pollResp0        = workflowservice.PollWorkflowTaskQueueResponse{
			Attempt:                1,
			WorkflowExecution:      &wfe,
			WorkflowType:           &wfType,
			History:                &history0,
			Messages:               messages,
			PreviousStartedEventId: 3,
		}
		task0     = workflowTask{task: &pollResp0}
		pollResp1 = workflowservice.PollWorkflowTaskQueueResponse{
			Attempt:                1,
			WorkflowExecution:      &wfe,
			WorkflowType:           &wfType,
			History:                &history1,
			PreviousStartedEventId: 3,
		}
		task1     = workflowTask{task: &pollResp1}
		pollResp2 = workflowservice.PollWorkflowTaskQueueResponse{
			Attempt:                1,
			WorkflowExecution:      &wfe,
			WorkflowType:           &wfType,
			History:                &history2,
			PreviousStartedEventId: 3,
		}
		task2 = workflowTask{task: &pollResp2}
	)

	// Return a workflow task to reset the workflow to a previous state
	client.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		Return(&workflowservice.RespondWorkflowTaskCompletedResponse{
			ResetHistoryEventId: 3,
		}, nil).Times(3)
	// Return a workflow task to complete the workflow
	client.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		Return(&workflowservice.RespondWorkflowTaskCompletedResponse{}, nil)

	poller := newWorkflowTaskPoller(taskHandler, contextManager, client, params)
	// Send a full history as part of the speculative WFT
	require.NoError(t, poller.processWorkflowTask(&task0))
	originalCachedExecution := cache.getWorkflowContext(runID)
	require.NotNil(t, originalCachedExecution)
	require.Equal(t, int64(3), originalCachedExecution.previousStartedEventID)
	require.Equal(t, int64(5), originalCachedExecution.lastHandledEventID)
	// Send some fake speculative WFTs to ensure the workflow is reset properly
	require.NoError(t, poller.processWorkflowTask(&task1))
	cachedExecution := cache.getWorkflowContext(runID)
	require.True(t, originalCachedExecution == cachedExecution)
	require.Equal(t, int64(3), cachedExecution.previousStartedEventID)
	require.Equal(t, int64(5), cachedExecution.lastHandledEventID)
	require.NoError(t, poller.processWorkflowTask(&task1))
	cachedExecution = cache.getWorkflowContext(runID)
	// Check the cached execution is the same as the original
	require.True(t, originalCachedExecution == cachedExecution)
	require.Equal(t, int64(3), cachedExecution.previousStartedEventID)
	require.Equal(t, int64(5), cachedExecution.lastHandledEventID)
	// Send a real WFT with new events
	require.NoError(t, poller.processWorkflowTask(&task2))
	cachedExecution = cache.getWorkflowContext(runID)
	require.True(t, originalCachedExecution == cachedExecution)
}
