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

package cadence

import (
	"testing"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/mocks"
	"go.uber.org/zap"
)

type (
	WorkersTestSuite struct {
		suite.Suite
	}
)

// Test suite.
func (s *WorkersTestSuite) SetupTest() {
}

func TestWorkersTestSuite(t *testing.T) {
	formatter := &log.TextFormatter{}
	formatter.FullTimestamp = true
	log.SetFormatter(formatter)
	log.SetLevel(log.DebugLevel)

	suite.Run(t, new(WorkersTestSuite))
}

func (s *WorkersTestSuite) TestWorkflowWorker() {
	domain := "testDomain"
	// mocks
	logger, _ := zap.NewDevelopment()
	service := new(mocks.TChanWorkflowService)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger: logger,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler(nil)}
	workflowWorker := newWorkflowWorkerInternal(testWorkflowDefinitionFactory, service, domain, executionParameters, nil, overrides)
	workflowWorker.Start()
	workflowWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorker() {
	domain := "testDomain"
	// mocks
	logger, _ := zap.NewDevelopment()
	service := new(mocks.TChanWorkflowService)
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(&m.PollForActivityTaskResponse{}, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger: logger,
	}
	overrides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler(nil)}
	activityWorker := newActivityWorker([]activity{&greeterActivity{}}, service, domain, executionParameters, overrides)
	activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestPollForDecisionTask_InternalServiceError() {
	domain := "testDomain"
	// mocks
	service := new(mocks.TChanWorkflowService)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, &m.InternalServiceError{})

	executionParameters := workerExecutionParameters{
		TaskList:                  "testDecisionTaskList",
		ConcurrentPollRoutineSize: 5,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler(nil)}
	workflowWorker := newWorkflowWorkerInternal(testWorkflowDefinitionFactory, service, domain, executionParameters, nil, overrides)
	workflowWorker.Start()
	workflowWorker.Stop()
}
