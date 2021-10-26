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
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
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
		service  *workflowservicemock.MockWorkflowServiceClient
	}
)

func helloWorldWorkflowFunc(ctx Context, _ []byte) error {
	queryResult := startingQueryValue
	_ = SetQueryHandler(ctx, queryType, func() (string, error) {
		return queryResult, nil
	})

	activityName := "Greeter_Activity"
	ao := ActivityOptions{
		TaskQueue:              "taskQueue",
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
	_ = SetQueryHandler(ctx, queryType, func() (string, error) {
		return queryResult, nil
	})

	_ = SetQueryHandler(ctx, errQueryType, func() (string, error) {
		return "", errors.New(queryErr)
	})

	ch := GetSignalChannel(ctx, signalCh)
	for i := 0; i < numSignals; i++ {
		// update queryResult when signal is received
		ch.Receive(ctx, &queryResult)

		// schedule activity to verify commands are produced
		ao := ActivityOptions{
			TaskQueue:              "taskQueue",
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
	result = append(result, GetWorkflowInfo(ctx).GetBinaryChecksum())
	_ = Sleep(ctx, time.Hour)
	result = append(result, GetWorkflowInfo(ctx).GetBinaryChecksum())
	_ = Sleep(ctx, time.Hour)
	result = append(result, GetWorkflowInfo(ctx).GetBinaryChecksum())
	return result, nil
}

func helloWorldWorkflowCancelFunc(ctx Context, _ []byte) error {
	activityName := "Greeter_Activity"
	ao := ActivityOptions{
		TaskQueue:              "taskQueue",
		ActivityID:             "0",
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	ExecuteActivity(ctx, activityName)
	getWorkflowEnvironment(ctx).RequestCancelActivity(ActivityID{"0"})
	return nil
}

// Greeter activity methods
func (ga greeterActivity) ActivityType() ActivityType {
	activityName := "Greeter_Activity"
	return ActivityType{Name: activityName}
}

func (ga greeterActivity) Execute(context.Context, *commonpb.Payloads) (*commonpb.Payloads, error) {
	return converter.GetDefaultDataConverter().ToPayloads([]byte("World"))
}

func (ga greeterActivity) GetFunction() interface{} {
	return ga.Execute
}

// Greeter activity func
func greeterActivityFunc(context.Context, []byte) ([]byte, error) {
	return []byte("Hello world"), nil
}

// Test suite.
func TestInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(InterfacesTestSuite))
}

func (s *InterfacesTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
}

func (s *InterfacesTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *InterfacesTestSuite) TestInterface() {
	namespace := "testNamespace"
	// Workflow execution parameters.
	workflowExecutionParameters := workerExecutionParameters{
		TaskQueue:                             "testTaskQueue",
		MaxConcurrentActivityTaskQueuePollers: 4,
		MaxConcurrentWorkflowTaskQueuePollers: 4,
		Logger:                                ilog.NewDefaultLogger(),
	}

	namespaceState := enumspb.NAMESPACE_STATE_REGISTERED
	namespaceDesc := &workflowservice.DescribeNamespaceResponse{
		NamespaceInfo: &namespacepb.NamespaceInfo{
			Name:  namespace,
			State: namespaceState,
		},
	}

	// mocks
	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(namespaceDesc, nil).AnyTimes()
	s.service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollActivityTaskQueueResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()
	s.service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollWorkflowTaskQueueResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.StartWorkflowExecutionResponse{}, nil).AnyTimes()

	registry := newRegistry()
	// Launch worker.
	workflowWorker := newWorkflowWorker(s.service, workflowExecutionParameters, nil, registry)
	defer workflowWorker.Stop()
	_ = workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := workerExecutionParameters{
		TaskQueue:                             "testTaskQueue",
		MaxConcurrentActivityTaskQueuePollers: 10,
		MaxConcurrentWorkflowTaskQueuePollers: 10,
		Logger:                                ilog.NewDefaultLogger(),
	}

	// Register activity instances and launch the worker.
	activityWorker := newActivityWorker(s.service, activityExecutionParameters, nil, registry, nil)
	defer activityWorker.Stop()
	_ = activityWorker.Start()

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		ID:                       "HelloWorld_Workflow",
		TaskQueue:                "testTaskQueue",
		WorkflowExecutionTimeout: 10 * time.Second,
		WorkflowTaskTimeout:      10 * time.Second,
	}
	workflowClient := NewServiceClient(s.service, nil, ClientOptions{})
	_, err := workflowClient.ExecuteWorkflow(context.Background(), workflowOptions, "WorkflowType")
	s.NoError(err)
}
