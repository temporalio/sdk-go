// Copyright (c) 2017 Uber Technologies, Inc.
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
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"
	"github.com/uber/tchannel-go/thrift"
	"golang.org/x/net/context"

	commonproto "github.com/temporalio/temporal-proto/common"
	"github.com/temporalio/temporal-proto/workflowservice"
	"github.com/temporalio/temporal-proto/workflowservicemock"
)

type (
	PollLayerInterfacesTestSuite struct {
		suite.Suite
		mockCtrl *gomock.Controller
		service  *workflowservicemock.MockWorkflowServiceYARPCClient
	}
)

// Sample Workflow task handler
type sampleWorkflowTaskHandler struct {
}

func (wth sampleWorkflowTaskHandler) ProcessWorkflowTask(
	workflowTask *workflowTask,
	d decisionHeartbeatFunc,
) (interface{}, error) {
	return &workflowservice.RespondDecisionTaskCompletedRequest{
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

func (ath sampleActivityTaskHandler) Execute(taskList string, task *workflowservice.PollForActivityTaskResponse) (interface{}, error) {
	activityImplementation := &greeterActivity{}
	result, err := activityImplementation.Execute(context.Background(), task.Input)
	if err != nil {
		reason := err.Error()
		return &workflowservice.RespondActivityTaskFailedRequest{
			TaskToken: task.TaskToken,
			Reason:    reason,
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
	s.service = workflowservicemock.NewMockWorkflowServiceYARPCClient(s.mockCtrl)
}

func (s *PollLayerInterfacesTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *PollLayerInterfacesTestSuite) TestProcessWorkflowTaskInterface() {
	ctx, _ := thrift.NewContext(10)

	// mocks
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any()).Return(&workflowservice.PollForDecisionTaskResponse{}, nil)
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any()).Return(nil, nil)

	response, err := s.service.PollForDecisionTask(ctx, &workflowservice.PollForDecisionTaskRequest{})
	s.NoError(err)

	// Process task and respond to the service.
	taskHandler := newSampleWorkflowTaskHandler()
	request, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: response}, nil)
	completionRequest := request.(*workflowservice.RespondDecisionTaskCompletedRequest)
	s.NoError(err)

	_, err = s.service.RespondDecisionTaskCompleted(ctx, completionRequest)
	s.NoError(err)
}

func (s *PollLayerInterfacesTestSuite) TestProcessActivityTaskInterface() {
	ctx, _ := thrift.NewContext(10)

	// mocks
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any()).Return(&workflowservice.PollForActivityTaskResponse{}, nil)
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any()).Return(nil)

	response, err := s.service.PollForActivityTask(ctx, &workflowservice.PollForActivityTaskRequest{})
	s.NoError(err)

	// Execute activity task and respond to the service.
	taskHandler := newSampleActivityTaskHandler()
	request, err := taskHandler.Execute(tasklist, response)
	s.NoError(err)
	switch request.(type) {
	case *workflowservice.RespondActivityTaskCompletedRequest:
		_, err = s.service.RespondActivityTaskCompleted(ctx, request.(*workflowservice.RespondActivityTaskCompletedRequest))
		s.NoError(err)
	case *workflowservice.RespondActivityTaskFailedRequest: // shouldn't happen
		_, err = s.service.RespondActivityTaskFailed(ctx, request.(*workflowservice.RespondActivityTaskFailedRequest))
		s.NoError(err)
	}
}

func (s *PollLayerInterfacesTestSuite) TestGetNextDecisions() {
	// Schedule an activity and see if we complete workflow.
	taskList := "tl1"
	testEvents := []*commonproto.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &commonproto.WorkflowExecutionStartedEventAttributes{TaskList: &commonproto.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskScheduled(2, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskStarted(3),
		{
			EventId:   4,
			EventType: commonproto.EventTypeDecisionTaskFailed,
		},
		{
			EventId:   5,
			EventType: commonproto.EventTypeWorkflowExecutionSignaled,
		},
		createTestEventDecisionTaskScheduled(6, &commonproto.DecisionTaskScheduledEventAttributes{TaskList: &commonproto.TaskList{Name: &taskList}}),
		createTestEventDecisionTaskStarted(7),
	}
	task := createWorkflowTask(testEvents[0:3], 0, "HelloWorld_Workflow")

	historyIterator := &historyIteratorImpl{
		iteratorFunc: func(nextToken []byte) (*commonproto.History, []byte, error) {
			return &commonproto.History{
				Events: testEvents[3:],
			}, nil, nil
		},
		nextPageToken: []byte("test"),
	}

	workflowTask := &workflowTask{task: task, historyIterator: historyIterator}

	eh := newHistory(workflowTask, nil)

	events, _, _, err := eh.NextDecisionEvents()

	s.NoError(err)
	s.Equal(3, len(events))
	s.Equal(commonproto.EventTypeWorkflowExecutionSignaled, events[1].GetEventType())
	s.Equal(commonproto.EventTypeDecisionTaskStarted, events[2].GetEventType())
	s.Equal(int64(7), events[2].GetEventId())
}
