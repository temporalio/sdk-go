package cadence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	m "code.uber.internal/devexp/cadence-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/cadence-client-go.git/mocks"
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
		cancelActivity bool
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

func (wf helloWorldWorkflow) Close() {

}

func (wf helloWorldWorkflow) Execute(env workflowEnvironment, input []byte) {
	activityName := "Greeter_Activity"
	activityParameters := ExecuteActivityParameters{
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
	logger := bark.NewLoggerFromLogrus(log.New())

	// Workflow execution parameters.
	workflowExecutionParameters := WorkerExecutionParameters{
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
	workflowWorker := newWorkflowWorker(testWorkflowDefinitionFactory, service, workflowExecutionParameters, nil)
	defer workflowWorker.Stop()
	workflowWorker.Start()

	// Create activity execution parameters.
	activityExecutionParameters := WorkerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 10,
		Logger: logger,
	}

	// Register activity instances and launch the worker.
	activityWorker := NewActivityWorker([]Activity{&greeterActivity{}}, service, activityExecutionParameters)
	defer activityWorker.Stop()
	activityWorker.Start()

	// Start a workflow.
	workflowOptions := StartWorkflowOptions{
		ID:       "HelloWorld_Workflow",
		TaskList: "testTaskList",
		Input:    nil,
		ExecutionStartToCloseTimeoutSeconds:    10,
		DecisionTaskStartToCloseTimeoutSeconds: 10,
	}
	workflowClient := NewWorkflowClient(service, nil)
	wfExecution, err := workflowClient.StartWorkflowExecution(workflowOptions)
	s.NoError(err)
	fmt.Printf("Started workflow: %v \n", wfExecution)
}
