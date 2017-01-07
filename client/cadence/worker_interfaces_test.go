package cadence

import (
	"errors"
	"fmt"
	"testing"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/mocks"
	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/uber-common/bark"
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

func (wf helloWorldWorkflow) Execute(env workflowEnvironment, input []byte) {
	activityName := "Greeter_Activity"
	activityParameters := ExecuteActivityParameters{
		TaskListName: "taskList",
		ActivityType: ActivityType{activityName},
		Input:        nil,
	}
	env.ExecuteActivity(activityParameters, func(result []byte, err Error) {
		if err != nil {
			env.Complete(nil, err)
			return
		}
		fmt.Println("Hello " + string(result) + "!")
		env.Complete(result, nil)
	})
}

// Greeter activity methods
func (ga greeterActivity) ActivityType() m.ActivityType {
	activityName := "Greeter_Activity"
	return m.ActivityType{Name: &activityName}
}
func (ga greeterActivity) Execute(context ActivityExecutionContext, input []byte) ([]byte, Error) {
	return []byte("World"), nil
}

// testWorkflowDefinitionFactory
func testWorkflowDefinitionFactory(workflowType WorkflowType) (WorkflowDefinition, Error) {
	return &helloWorldWorkflow{}, nil
}

// testActivityImplementationFactory
func testActivityImplementationFactory(activityType ActivityType) (ActivityImplementation, Error) {
	return &greeterActivity{}, nil
}

// Test suite.
func (s *InterfacesTestSuite) SetupTest() {
}

func TestInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(InterfacesTestSuite))
}

func (s *InterfacesTestSuite) TestInterface() {
	logger := bark.NewLoggerFromLogrus(log.New())

	// Workflow execution parameters.
	workflowExecutionParameters := WorkerExecutionParameters{}
	workflowExecutionParameters.TaskListName = "testTaskList"
	workflowExecutionParameters.ConcurrentPollRoutineSize = 4

	// Create service endpoint
	service := new(mocks.TChanWorkflowService)

	// mocks
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(&m.PollForActivityTaskResponse{}, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)
	service.On("StartWorkflowExecution", mock.Anything, mock.Anything).Return(&m.StartWorkflowExecutionResponse{}, nil)

	// Launch worker.
	workflowWorker := NewWorkflowWorker(workflowExecutionParameters, testWorkflowDefinitionFactory, service, logger, nil, nil)
	defer workflowWorker.Shutdown()
	workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := WorkerExecutionParameters{}
	activityExecutionParameters.TaskListName = "testTaskList"
	activityExecutionParameters.ConcurrentPollRoutineSize = 10

	// Register activity instances and launch the worker.
	activityWorker := NewActivityWorker(activityExecutionParameters, testActivityImplementationFactory, service, logger, nil)
	defer activityWorker.Shutdown()
	activityWorker.Start()

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		WorkflowID:                             "HelloWorld_Workflow",
		TaskListName:                           "testTaskList",
		WorkflowInput:                          nil,
		ExecutionStartToCloseTimeoutSeconds:    10,
		DecisionTaskStartToCloseTimeoutSeconds: 10,
	}
	workflowClient := NewWorkflowClient(workflowOptions, service, nil)
	wfExecution, err := workflowClient.StartWorkflowExecution()
	s.NoError(err)
	fmt.Printf("Started workflow: %v \n", wfExecution)
}
