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
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	commonproto "github.com/temporalio/temporal-proto/common"
	"github.com/temporalio/temporal-proto/enums"
	"github.com/temporalio/temporal-proto/workflowservice"
	"github.com/temporalio/temporal-proto/workflowservicemock"
)

const (
	queryType    = "test-query"
	errQueryType = "test-err-query"
	signalCh     = "signal-chan"

	startingQueryValue = ""
	finishedQueryValue = "done"
	queryErr           = "error handling query"
)

type (
	// Greeter activity
	greeterActivity struct {
	}

	InterfacesTestSuite struct {
		suite.Suite
		mockCtrl *gomock.Controller
		service  *workflowservicemock.MockWorkflowServiceYARPCClient
	}
)

func helloWorldWorkflowFunc(ctx Context, input []byte) error {
	queryResult := startingQueryValue
	SetQueryHandler(ctx, queryType, func() (string, error) {
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
		queryResult = finishedQueryValue
		return nil
	}

	queryResult = "error:" + err.Error()
	return err
}

func querySignalWorkflowFunc(ctx Context, numSignals int) error {
	queryResult := startingQueryValue
	SetQueryHandler(ctx, queryType, func() (string, error) {
		return queryResult, nil
	})

	SetQueryHandler(ctx, errQueryType, func() (string, error) {
		return "", errors.New(queryErr)
	})

	ch := GetSignalChannel(ctx, signalCh)
	for i := 0; i < numSignals; i++ {
		// update queryResult when signal is received
		ch.Receive(ctx, &queryResult)

		// schedule activity to verify decisions are produced
		ao := ActivityOptions{
			TaskList:               "taskList",
			ActivityID:             "0",
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       20 * time.Second,
		}
		ExecuteActivity(WithActivityOptions(ctx, ao), "Greeter_Activity")
	}
	return nil
}

func binaryChecksumWorkflowFunc(ctx Context) ([]string, error) {
	var result []string
	result = append(result, GetWorkflowInfo(ctx).BinaryChecksum)
	Sleep(ctx, time.Hour)
	result = append(result, GetWorkflowInfo(ctx).BinaryChecksum)
	Sleep(ctx, time.Hour)
	result = append(result, GetWorkflowInfo(ctx).BinaryChecksum)
	return result, nil
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
func TestInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(InterfacesTestSuite))
}

func (s *InterfacesTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceYARPCClient(s.mockCtrl)
}

func (s *InterfacesTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *InterfacesTestSuite) TestInterface() {
	logger, _ := zap.NewDevelopment()
	domain := "testDomain"
	// Workflow execution parameters.
	workflowExecutionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 4,
		Logger:                    logger,
		Tracer:                    opentracing.NoopTracer{},
	}

	domainStatus := enums.DomainStatusRegistered
	domainDesc := &workflowservice.DescribeDomainResponse{
		DomainInfo: &commonproto.DomainInfo{
			Name:   domain,
			Status: domainStatus,
		},
	}

	// mocks
	s.service.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), callOptions...).Return(domainDesc, nil).AnyTimes()
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), callOptions...).Return(&workflowservice.PollForActivityTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), callOptions...).Return(&workflowservice.PollForDecisionTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), callOptions...).Return(nil, nil).AnyTimes()
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), callOptions...).Return(&workflowservice.StartWorkflowExecutionResponse{}, nil).AnyTimes()

	registry := getGlobalRegistry()
	// Launch worker.
	workflowWorker := newWorkflowWorker(s.service, domain, workflowExecutionParameters, nil, registry)
	defer workflowWorker.Stop()
	workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 10,
		Logger:                    logger,
		Tracer:                    opentracing.NoopTracer{},
	}

	// Register activity instances and launch the worker.
	activityWorker := newActivityWorker(s.service, domain, activityExecutionParameters, nil, registry, nil)
	defer activityWorker.Stop()
	activityWorker.Start()

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		ID:                              "HelloWorld_Workflow",
		TaskList:                        "testTaskList",
		ExecutionStartToCloseTimeout:    10 * time.Second,
		DecisionTaskStartToCloseTimeout: 10 * time.Second,
	}
	workflowClient := NewClient(s.service, domain, nil)
	_, err := workflowClient.StartWorkflow(context.Background(), workflowOptions, "workflowType")
	s.NoError(err)
}
