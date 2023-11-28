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
	"sync/atomic"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"
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
