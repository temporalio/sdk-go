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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/zap"
)

var (
	// ErrWorkflowTypeNotExist indicated workflow type doesn't exist in registry.
	ErrWorkflowTypeNotExist = errors.New("workflow type doesn't existin the registry")
)

type (
	// Workflow decider
	helloWorldWorkflow struct {
		cancelActivity bool
	}

	// Greeter activity
	greeterActivity struct {
	}

	InterfacesTestSuite struct {
		suite.Suite
	}
)

func helloWorldWorkflowFunc(ctx Context, input []byte) error {
	var queryResult string
	SetQueryHandler(ctx, "test-query", func() (string, error) {
		return queryResult, nil
	})

	activityName := "Greeter_Activity"
	ao := ActivityOptions{
		TaskList:               "taskList",
		ActivityID:             "0",
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	var result []byte
	queryResult = "waiting-activity-result"
	err := ExecuteActivity(ctx, activityName).Get(ctx, &result)
	if err == nil {
		queryResult = "done"
		return nil
	}

	queryResult = "error:" + err.Error()
	return err
}

func helloWorldWorkflowCancelFunc(ctx Context, input []byte) error {
	activityName := "Greeter_Activity"
	ao := ActivityOptions{
		TaskList:               "taskList",
		ActivityID:             "0",
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	ExecuteActivity(ctx, activityName)
	getWorkflowEnvironment(ctx).RequestCancelActivity("0")
	return nil
}

// Greeter activity methods
func (ga greeterActivity) ActivityType() ActivityType {
	activityName := "Greeter_Activity"
	return ActivityType{Name: activityName}
}

func (ga greeterActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("World"), nil
}

func (ga greeterActivity) GetFunction() interface{} {
	return ga.Execute
}

// Greeter activity func
func greeterActivityFunc(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("Hello world"), nil
}

// Test suite.
func (s *InterfacesTestSuite) SetupTest() {
}

func TestInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(InterfacesTestSuite))
}

func (s *InterfacesTestSuite) TestInterface() {
	logger, _ := zap.NewDevelopment()
	domain := "testDomain"
	// Workflow execution parameters.
	workflowExecutionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 4,
		Logger: logger,
	}

	// Create service endpoint
	mockCtrl := gomock.NewController(s.T())
	service := workflowservicetest.NewMockClient(mockCtrl)

	domainStatus := m.DomainStatusRegistered
	domainDesc := &m.DescribeDomainResponse{
		DomainInfo: &m.DomainInfo{
			Name:   &domain,
			Status: &domainStatus,
		},
	}

	// mocks
	service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(domainDesc, nil).AnyTimes()
	service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForActivityTaskResponse{}, nil).AnyTimes()
	service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()
	service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&m.PollForDecisionTaskResponse{}, nil).AnyTimes()
	service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil).AnyTimes()
	service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), callOptions...).Return(&m.StartWorkflowExecutionResponse{}, nil).AnyTimes()

	env := getHostEnvironment()
	// Launch worker.
	workflowWorker := newWorkflowWorker(service, domain, workflowExecutionParameters, nil, env)
	defer workflowWorker.Stop()
	workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 10,
		Logger: logger,
	}

	// Register activity instances and launch the worker.
	activityWorker := newActivityWorker(service, domain, activityExecutionParameters, nil, env)
	defer activityWorker.Stop()
	activityWorker.Start()

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		ID:                              "HelloWorld_Workflow",
		TaskList:                        "testTaskList",
		ExecutionStartToCloseTimeout:    10 * time.Second,
		DecisionTaskStartToCloseTimeout: 10 * time.Second,
	}
	workflowClient := NewClient(service, domain, nil)
	wfExecution, err := workflowClient.StartWorkflow(context.Background(), workflowOptions, "workflowType")
	s.NoError(err)
	fmt.Printf("Started workflow: %v \n", wfExecution)
}
