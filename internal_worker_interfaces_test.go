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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	m "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/mocks"
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

// Workflow methods.
func (wf helloWorldWorkflow) WorkflowType() m.WorkflowType {
	workflowName := "HelloWorld_Workflow"
	return m.WorkflowType{Name: &workflowName}
}

func (wf helloWorldWorkflow) StackTrace() string {
	return ""
}

func (wf helloWorldWorkflow) Close() {

}

func (wf helloWorldWorkflow) OnDecisionTaskStarted() {

}

func (wf helloWorldWorkflow) Execute(env workflowEnvironment, input []byte) {
	activityName := "Greeter_Activity"
	activityParameters := executeActivityParameters{
		TaskListName: "taskList",
		ActivityType: ActivityType{activityName},
		Input:        nil,
	}
	a := env.ExecuteActivity(activityParameters, func(result []byte, err error) {
		if err != nil {
			if _, ok := err.(CanceledError); !ok {
				env.Complete(nil, err)
				return
			}
		}
		fmt.Println("Hello " + string(result) + "!")
		env.Complete(result, nil)
	})
	if wf.cancelActivity {
		env.RequestCancelActivity(a.activityID)
	}
}

// Greeter activity methods
func (ga greeterActivity) ActivityType() ActivityType {
	activityName := "Greeter_Activity"
	return ActivityType{Name: activityName}
}
func (ga greeterActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("World"), nil
}

// testWorkflowDefinitionFactory
func testWorkflowDefinitionFactory(workflowType WorkflowType) (workflowDefinition, error) {
	return &helloWorldWorkflow{}, nil
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
	service := new(mocks.TChanWorkflowService)

	// mocks
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(&m.PollForActivityTaskResponse{}, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)
	service.On("StartWorkflowExecution", mock.Anything, mock.Anything).Return(&m.StartWorkflowExecutionResponse{}, nil)

	// Launch worker.
	workflowWorker := newWorkflowWorker(testWorkflowDefinitionFactory, service, domain, workflowExecutionParameters, nil)
	defer workflowWorker.Stop()
	workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 10,
		Logger: logger,
	}

	// Register activity instances and launch the worker.
	activityWorker := newActivityWorker([]activity{&greeterActivity{}}, service, domain, activityExecutionParameters, nil)
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
	wfExecution, err := workflowClient.StartWorkflow(workflowOptions, "workflowType")
	s.NoError(err)
	fmt.Printf("Started workflow: %v \n", wfExecution)
}
