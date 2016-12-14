package examples

import (
	"encoding/json"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/client/flow"
	"code.uber.internal/devexp/minions-client-go.git/common"
	log "github.com/Sirupsen/logrus"
)

type (
	// WorkflowHelper class for workflow helpers.
	WorkflowHelper struct {
		service        m.TChanWorkflowService
		workflowWorker *flow.WorkflowWorker
		activityWorker *flow.ActivityWorker
	}
)

var workflowFactory = func(wt m.WorkflowType) (flow.WorkflowDefinition, flow.Error) {
	switch wt.GetName() {
	case "greetingsWorkflow":
		return flow.NewWorkflowDefinition(greetingsWorkflow{}), nil
	}
	panic("Invalid workflow type")
}

var activityFactory = func(at m.ActivityType) (flow.ActivityImplementation, flow.Error) {
	switch at.GetName() {
	case "getGreetingActivity":
		return getGreetingActivity{}, nil
	case "getNameActivity":
		return getNameActivity{}, nil
	case "sayGreetingActivity":
		return sayGreetingActivity{}, nil
	}
	panic("Invalid activity type")
}

func activityInfo(activityName string) flow.ExecuteActivityParameters {
	return serializeParams(activityName, nil)
}

func activityInfoWithInput(activityName string, request *sayGreetingActivityRequest) flow.ExecuteActivityParameters {
	sayGreetInput, err := json.Marshal(request)
	if err != nil {
		log.Panicf("Marshalling failed with error: %+v", err)
	}
	return serializeParams(activityName, sayGreetInput)
}

func serializeParams(activityName string, input []byte) flow.ExecuteActivityParameters {
	return flow.ExecuteActivityParameters{
		TaskListName: "exampleTaskList",
		ActivityType: m.ActivityType{Name: common.StringPtr(activityName)},
		Input:        input}
}

// NewWorkflowHelper creates a helper.
func NewWorkflowHelper(service m.TChanWorkflowService) *WorkflowHelper {
	return &WorkflowHelper{service: service}
}

// StartWorkers starts necessary workers.
func (w *WorkflowHelper) StartWorkers() {
	logger := log.WithFields(log.Fields{})

	// Workflow execution parameters.
	workflowExecutionParameters := flow.WorkerExecutionParameters{}
	workflowExecutionParameters.TaskListName = "exampleTaskList"
	workflowExecutionParameters.ConcurrentPollRoutineSize = 4

	// Launch worker.
	w.workflowWorker = flow.NewWorkflowWorker(workflowExecutionParameters, workflowFactory, w.service, logger)
	w.workflowWorker.Start()
	log.Infoln("Started Deciders for workflows.")

	// Create activity execution parameters.
	activityExecutionParameters := flow.WorkerExecutionParameters{}
	activityExecutionParameters.TaskListName = "exampleTaskList"
	activityExecutionParameters.ConcurrentPollRoutineSize = 10

	// Register activity instances and launch the worker.
	w.activityWorker = flow.NewActivityWorker(activityExecutionParameters, activityFactory, w.service, logger)
	w.activityWorker.Start()
	log.Infoln("Started activities for workflows.")
}

// StopWorkers stops necessary workers.
func (w *WorkflowHelper) StopWorkers() {
	if w.workflowWorker != nil {
		w.workflowWorker.Shutdown()
	}

	if w.activityWorker != nil {
		w.activityWorker.Shutdown()
	}
}

// StartWorkflow starts an workflow instance.
func (w *WorkflowHelper) StartWorkflow(workflowName string) {
	workflowOptions := flow.StartWorkflowOptions{
		WorkflowID:                             "examples-greetingWorkflow",
		WorkflowType:                           m.WorkflowType{Name: common.StringPtr(workflowName)},
		TaskListName:                           "exampleTaskList",
		WorkflowInput:                          nil,
		ExecutionStartToCloseTimeoutSeconds:    10,
		DecisionTaskStartToCloseTimeoutSeconds: 10,
	}
	workflowClient := flow.NewWorkflowClient(workflowOptions, w.service)
	we, err := workflowClient.StartWorkflowExecution()
	if err != nil {
		log.Panicf("Failed to start workflow: %s, with error: %s.\n", workflowName, err.Error())
	}
	log.Infof("Created Workflow - workflow Id: %s, run Id: %s.\n", we.GetWorkflowId(), we.GetRunId())
}
