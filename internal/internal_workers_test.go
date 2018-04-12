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
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/zap"
)

type (
	WorkersTestSuite struct {
		suite.Suite
		mockCtrl *gomock.Controller
		service  *workflowservicetest.MockClient
	}
)

// Test suite.
func (s *WorkersTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicetest.NewMockClient(s.mockCtrl)
}

func (s *WorkersTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
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
	logger, _ := zap.NewDevelopment()

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForDecisionTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger: logger,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(
		s.service, domain, executionParameters, nil, overrides, getHostEnvironment(),
	)
	workflowWorker.Start()
	workflowWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorker() {
	domain := "testDomain"
	logger, _ := zap.NewDevelopment()

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForActivityTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger: logger,
	}
	overrides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler()}
	a := &greeterActivity{}
	hostEnv := getHostEnvironment()
	hostEnv.addActivity(a.ActivityType().Name, a)
	activityWorker := newActivityWorker(
		s.service, domain, executionParameters, overrides, hostEnv,
	)
	activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestPollForDecisionTask_InternalServiceError() {
	domain := "testDomain"

	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil)
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForDecisionTaskResponse{}, &m.InternalServiceError{}).AnyTimes()

	executionParameters := workerExecutionParameters{
		TaskList:                  "testDecisionTaskList",
		ConcurrentPollRoutineSize: 5,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler()}
	workflowWorker := newWorkflowWorkerInternal(
		s.service, domain, executionParameters, nil, overrides, getHostEnvironment(),
	)
	workflowWorker.Start()
	workflowWorker.Stop()
}
