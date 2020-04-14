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
	"github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/temporal-proto/common"
	"go.uber.org/zap"

	namespacepb "go.temporal.io/temporal-proto/namespace"
	"go.temporal.io/temporal-proto/workflowservice"
	"go.temporal.io/temporal-proto/workflowservicemock"
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
	_ = Sleep(ctx, time.Hour)
	result = append(result, GetWorkflowInfo(ctx).BinaryChecksum)
	_ = Sleep(ctx, time.Hour)
	result = append(result, GetWorkflowInfo(ctx).BinaryChecksum)
	return result, nil
}

func helloWorldWorkflowCancelFunc(ctx Context, _ []byte) error {
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

func (ga greeterActivity) Execute(context.Context, *commonpb.Payload) (*commonpb.Payload, error) {
	return &commonpb.Payload{Items: []*commonpb.PayloadItem{{
		Metadata: map[string][]byte{
			encodingMetadata: []byte(encodingMetadataRaw),
		},
		Data: []byte("World"),
	}}}, nil
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
	logger, _ := zap.NewDevelopment()
	namespace := "testNamespace"
	// Workflow execution parameters.
	workflowExecutionParameters := workerExecutionParameters{
		TaskList:                     "testTaskList",
		MaxConcurrentActivityPollers: 4,
		MaxConcurrentDecisionPollers: 4,
		Logger:                       logger,
		Tracer:                       opentracing.NoopTracer{},
	}

	namespaceStatus := namespacepb.NamespaceStatus_Registered
	namespaceDesc := &workflowservice.DescribeNamespaceResponse{
		NamespaceInfo: &namespacepb.NamespaceInfo{
			Name:   namespace,
			Status: namespaceStatus,
		},
	}

	// mocks
	s.service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(namespaceDesc, nil).AnyTimes()
	s.service.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForActivityTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()
	s.service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.PollForDecisionTaskResponse{}, nil).AnyTimes()
	s.service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.StartWorkflowExecutionResponse{}, nil).AnyTimes()

	registry := newRegistry()
	// Launch worker.
	workflowWorker := newWorkflowWorker(s.service, workflowExecutionParameters, nil, registry)
	defer workflowWorker.Stop()
	_ = workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := workerExecutionParameters{
		TaskList:                     "testTaskList",
		MaxConcurrentActivityPollers: 10,
		MaxConcurrentDecisionPollers: 10,
		Logger:                       logger,
		Tracer:                       opentracing.NoopTracer{},
	}

	// Register activity instances and launch the worker.
	activityWorker := newActivityWorker(s.service, activityExecutionParameters, nil, registry, nil)
	defer activityWorker.Stop()
	_ = activityWorker.Start()

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		ID:                              "HelloWorld_Workflow",
		TaskList:                        "testTaskList",
		ExecutionStartToCloseTimeout:    10 * time.Second,
		DecisionTaskStartToCloseTimeout: 10 * time.Second,
	}
	workflowClient := NewServiceClient(s.service, nil, ClientOptions{})
	_, err := workflowClient.ExecuteWorkflow(context.Background(), workflowOptions, "workflowType")
	s.NoError(err)
}
