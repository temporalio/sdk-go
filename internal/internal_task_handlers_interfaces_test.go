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
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
)

type (
	PollLayerInterfacesTestSuite struct {
		suite.Suite
		mockCtrl *gomock.Controller
		service  *workflowservicemock.MockWorkflowServiceClient
	}
)

// Sample Workflow task handler
type sampleWorkflowTaskHandler struct {
}

func (wth sampleWorkflowTaskHandler) ProcessWorkflowTask(
	workflowTask *workflowTask,
	_ workflowTaskHeartbeatFunc,
) (interface{}, error) {
	return &workflowservice.RespondWorkflowTaskCompletedRequest{
		TaskToken: workflowTask.task.TaskToken,
	}, nil
}

func newSampleWorkflowTaskHandler() *sampleWorkflowTaskHandler {
	return &sampleWorkflowTaskHandler{}
}

// Sample ActivityTaskHandler
type sampleActivityTaskHandler struct {
}

func newSampleActivityTaskHandler() *sampleActivityTaskHandler {
	return &sampleActivityTaskHandler{}
}

func (ath sampleActivityTaskHandler) Execute(_ string, task *workflowservice.PollActivityTaskQueueResponse) (interface{}, error) {
	activityImplementation := &greeterActivity{}
	result, err := activityImplementation.Execute(context.Background(), task.Input)
	if err != nil {
		failure := ConvertErrorToFailure(NewApplicationError(err.Error(), getErrType(err), false, nil), converter.GetDefaultDataConverter())
		return &workflowservice.RespondActivityTaskFailedRequest{
			TaskToken: task.TaskToken,
			Failure:   failure,
		}, nil
	}
	return &workflowservice.RespondActivityTaskCompletedRequest{
		TaskToken: task.TaskToken,
		Result:    result,
	}, nil
}

// Test suite.
func TestPollLayerInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(PollLayerInterfacesTestSuite))
}

func (s *PollLayerInterfacesTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
}

func (s *PollLayerInterfacesTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *PollLayerInterfacesTestSuite) TestProcessWorkflowTaskInterface() {
	ctx, cancel := context.WithTimeout(context.Background(), 10)
	defer cancel()

	// mocks
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any()).Return(&workflowservice.PollWorkflowTaskQueueResponse{}, nil)
	s.service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).Return(nil, nil)

	response, err := s.service.PollWorkflowTaskQueue(ctx, &workflowservice.PollWorkflowTaskQueueRequest{})
	s.NoError(err)

	// Process task and respond to the service.
	taskHandler := newSampleWorkflowTaskHandler()
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: response}, nil)
	completionRequest := request.(*workflowservice.RespondWorkflowTaskCompletedRequest)
	s.NoError(err)

	_, err = s.service.RespondWorkflowTaskCompleted(ctx, completionRequest)
	s.NoError(err)
}

func (s *PollLayerInterfacesTestSuite) TestProcessActivityTaskInterface() {
	ctx, cancel := context.WithTimeout(context.Background(), 10)
	defer cancel()

	// mocks
	s.service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any()).Return(&workflowservice.PollActivityTaskQueueResponse{}, nil)
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil)

	response, err := s.service.PollActivityTaskQueue(ctx, &workflowservice.PollActivityTaskQueueRequest{})
	s.NoError(err)

	// Execute activity task and respond to the service.
	taskHandler := newSampleActivityTaskHandler()
	request, err := taskHandler.Execute(taskqueue, response)
	s.NoError(err)
	switch request := request.(type) {
	case *workflowservice.RespondActivityTaskCompletedRequest:
		_, err = s.service.RespondActivityTaskCompleted(ctx, request)
		s.NoError(err)
	case *workflowservice.RespondActivityTaskFailedRequest: // shouldn't happen
		_, err = s.service.RespondActivityTaskFailed(ctx, request)
		s.NoError(err)
	}
}

func (s *PollLayerInterfacesTestSuite) TestGetNextCommands() {
	// Schedule an activity and see if we complete workflow.
	taskQueue := "tq1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(3),
		{
			EventId:   4,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED,
		},
		{
			EventId:   5,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED,
		},
		createTestEventWorkflowTaskScheduled(6, &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}}),
		createTestEventWorkflowTaskStarted(7),
	}
	task := createWorkflowTaskWithQueries(testEvents[0:3], 0, "HelloWorld_Workflow", nil, false)

	historyIterator := &historyIteratorImpl{
		iteratorFunc: func(nextToken []byte) (*historypb.History, []byte, error) {
			return &historypb.History{
				Events: testEvents[3:],
			}, nil, nil
		},
		nextPageToken: []byte("test"),
	}

	workflowTask := &workflowTask{task: task, historyIterator: historyIterator}

	eh := newHistory(workflowTask, nil)

	events, _, _, err := eh.NextCommandEvents()

	s.NoError(err)
	s.Equal(3, len(events))
	s.Equal(enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED, events[1].GetEventType())
	s.Equal(enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED, events[2].GetEventType())
	s.Equal(int64(7), events[2].GetEventId())
}
