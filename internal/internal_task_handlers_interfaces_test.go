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
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	m "go.uber.org/cadence/.gen/go/shared"
	"golang.org/x/net/context"
)

type (
	PollLayerInterfacesTestSuite struct {
		suite.Suite
	}
)

// Workflow Context
type testWorkflowContext struct {
}

func (wc testWorkflowContext) WorkflowInfo() WorkflowInfo {
	return WorkflowInfo{}
}
func (wc testWorkflowContext) Complete(result []byte) {
}
func (wc testWorkflowContext) Fail(err error) {
}
func (wc testWorkflowContext) ScheduleActivityTask(parameters executeActivityParameters, callback resultHandler) {

}

// Activity Execution context
type testActivityExecutionContext struct {
}

func (ac testActivityExecutionContext) TaskToken() []byte {
	return []byte("")
}
func (ac testActivityExecutionContext) RecordActivityHeartbeat(details []byte) error {
	return nil
}

// Sample Workflow task handler
type sampleWorkflowTaskHandler struct {
}

func (wth sampleWorkflowTaskHandler) ProcessWorkflowTask(
	task *m.PollForDecisionTaskResponse,
	historyIterator HistoryIterator,
	emitStack bool) (interface{}, string, error) {
	return &m.RespondDecisionTaskCompletedRequest{
		TaskToken: task.TaskToken,
	}, "", nil
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

func (ath sampleActivityTaskHandler) Execute(task *m.PollForActivityTaskResponse) (interface{}, error) {
	activityImplementation := &greeterActivity{}
	result, err := activityImplementation.Execute(context.Background(), task.Input)
	if err != nil {
		reason := err.Error()
		return &m.RespondActivityTaskFailedRequest{
			TaskToken: task.TaskToken,
			Reason:    &reason,
		}, nil
	}
	return &m.RespondActivityTaskCompletedRequest{
		TaskToken: task.TaskToken,
		Result:    result,
	}, nil
}

// Test suite.
func (s *PollLayerInterfacesTestSuite) SetupTest() {
}

func TestPollLayerInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(PollLayerInterfacesTestSuite))
}

func (s *PollLayerInterfacesTestSuite) TestProcessWorkflowTaskInterface() {
	// Create service endpoint and get a workflow task.
	mockCtrl := gomock.NewController(s.T())
	service := workflowservicetest.NewMockClient(mockCtrl)
	ctx, _ := thrift.NewContext(10)

	// mocks
	service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any()).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any()).Return(nil)

	response, err := service.PollForDecisionTask(ctx, &m.PollForDecisionTaskRequest{})
	s.NoError(err)

	// Process task and respond to the service.
	taskHandler := newSampleWorkflowTaskHandler()
	request, _, err := taskHandler.ProcessWorkflowTask(response, nil, false)
	completionRequest := request.(*m.RespondDecisionTaskCompletedRequest)
	s.NoError(err)

	err = service.RespondDecisionTaskCompleted(ctx, completionRequest)
	s.NoError(err)
}

func (s *PollLayerInterfacesTestSuite) TestProcessActivityTaskInterface() {
	// Create service endpoint and get a activity task.
	mockCtrl := gomock.NewController(s.T())
	service := workflowservicetest.NewMockClient(mockCtrl)

	ctx, _ := thrift.NewContext(10)

	// mocks
	service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any()).Return(&m.PollForActivityTaskResponse{}, nil)
	service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any()).Return(nil)

	response, err := service.PollForActivityTask(ctx, &m.PollForActivityTaskRequest{})
	s.NoError(err)

	// Execute activity task and respond to the service.
	taskHandler := newSampleActivityTaskHandler()
	request, err := taskHandler.Execute(response)
	s.NoError(err)
	switch request.(type) {
	case m.RespondActivityTaskCompletedRequest:
		err = service.RespondActivityTaskCompleted(ctx, request.(*m.RespondActivityTaskCompletedRequest))
		s.NoError(err)
	case m.RespondActivityTaskFailedRequest:
		err = service.RespondActivityTaskFailed(ctx, request.(*m.RespondActivityTaskFailedRequest))
		s.NoError(err)
	}
}
