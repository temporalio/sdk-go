package flow

import (
	"errors"
	"fmt"
	"testing"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/mocks"
	"github.com/stretchr/testify/suite"
)

var (
	// ErrWorkflowTypeNotExist indicated workflow type doesn't exist in registry.
	ErrWorkflowTypeNotExist = errors.New("workflow type doesn't existin the registry")
)

type (
	// Workflow decider
	helloWorldWorkflow struct {
	}

	// Greeter Activity
	greeeterActivity struct {
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

func (wf helloWorldWorkflow) Execute(context WorkflowContext, input []byte) {
	activityName := "Greeter_Activity"
	activityParameters := ExecuteActivityParameters{
		ActivityID:   "activity_id",
		TaskListName: "taskList",
		ActivityType: m.ActivityType{&activityName},
		Input:        nil,
	}
	context.ScheduleActivityTask(activityParameters, func(err error, result []byte) {
		if err != nil {
			context.Fail(err)
			return
		}
		fmt.Println("Hello " + string(result) + "!")
		context.Complete(nil)
	})
}

// Greeter activity methods
func (ga greeeterActivity) ActivityType() m.ActivityType {
	activityName := "Greeter_Activity"
	return m.ActivityType{Name: &activityName}
}
func (ga greeeterActivity) Execute(context ActivityExecutionContext, input []byte) ([]byte, error) {
	return []byte("World"), nil
}

// WorkflowDefinitionFactory
type workflowDefinitionFactory struct {
}

func (wdf workflowDefinitionFactory) GetWorkflowDefinition(workflowType m.WorkflowType) (WorkflowDefinition, error) {
	return &helloWorldWorkflow{}, nil
}

// Test suite.
func (s *InterfacesTestSuite) SetupTest() {
}

func TestWorkersTestSuite(t *testing.T) {
	suite.Run(t, new(InterfacesTestSuite))
}

func (s *InterfacesTestSuite) TestInterface() {
	// Workflow execution parameters.
	workflowExecutionParameters := WorkerExecutionParameters{}
	workflowExecutionParameters.TaskListName = "testTaskList"
	workflowExecutionParameters.ConcurrentPollingSize = 4
	workflowExecutionParameters.TaskExecutorPoolSize = 4

	// Create service endpoint
	service := new(mocks.TChanWorkflowService)

	// Launch worker.
	workflowWorker := NewWorkflowWorker(workflowExecutionParameters, workflowDefinitionFactory{}, service)
	err := workflowWorker.Start()
	s.NoError(err, "Failed to start workflow worker")

	// Create activity execution parameters.
	activityExecutionParameters := WorkerExecutionParameters{}
	activityExecutionParameters.TaskListName = "testTaskList"
	activityExecutionParameters.ConcurrentPollingSize = 10
	activityExecutionParameters.TaskExecutorPoolSize = 100

	// Register activity instances and launch the worker.
	activityWorker := NewActivityWorker(activityExecutionParameters, service)
	activityWorker.AddActivityImplementationInstance(&greeeterActivity{})
	err = activityWorker.Start()
	s.NoError(err, "Failed to start activity worker")

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		WorkflowID:                             "HelloWorld_Workflow",
		TaskListName:                           "testTaskList",
		WorkflowInput:                          nil,
		ExecutionStartToCloseTimeoutSeconds:    10,
		DecisionTaskStartToCloseTimeoutSeconds: 10,
	}
	workflowClient := NewWorkflowClient(workflowOptions)
	wfExecution, err := workflowClient.StartWorkflowExecution()
	s.NoError(err)
	fmt.Printf("Started workflow: %v \n", wfExecution)
}
