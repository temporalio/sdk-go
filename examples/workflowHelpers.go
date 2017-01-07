package examples

import (
	"encoding/json"

	log "github.com/Sirupsen/logrus"
	"github.com/uber-common/bark"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
)

type (
	// WorkflowHelper class for workflow helpers.
	WorkflowHelper struct {
		service        m.TChanWorkflowService
		workflowWorker *cadence.WorkflowWorker
		activityWorker *cadence.ActivityWorker
	}
)

var workflowFactory = func(wt cadence.WorkflowType) (cadence.WorkflowDefinition, cadence.Error) {
	switch wt.Name {
	case "greetingsWorkflow":
		return cadence.NewWorkflowDefinition(greetingsWorkflow{}), nil
	}
	panic("Invalid workflow type")
}

var activityFactory = func(at cadence.ActivityType) (cadence.ActivityImplementation, cadence.Error) {
	switch at.Name {
	case "getGreetingActivity":
		return getGreetingActivity{}, nil
	case "getNameActivity":
		return getNameActivity{}, nil
	case "sayGreetingActivity":
		return sayGreetingActivity{}, nil
	}
	panic("Invalid activity type")
}

func activityInfo(activityName string) cadence.ExecuteActivityParameters {
	return serializeParams(activityName, nil)
}

func activityInfoWithInput(activityName string, request *sayGreetingActivityRequest) cadence.ExecuteActivityParameters {
	sayGreetInput, err := json.Marshal(request)
	if err != nil {
		log.Panicf("Marshalling failed with error: %+v", err)
	}
	return serializeParams(activityName, sayGreetInput)
}

func serializeParams(activityName string, input []byte) cadence.ExecuteActivityParameters {
	return cadence.ExecuteActivityParameters{
		TaskListName: "exampleTaskList",
		ActivityType: cadence.ActivityType{Name: activityName},
		Input:        input}
}

// NewWorkflowHelper creates a helper.
func NewWorkflowHelper(service m.TChanWorkflowService) *WorkflowHelper {
	return &WorkflowHelper{service: service}
}

// StartWorkers starts necessary workers.
func (w *WorkflowHelper) StartWorkers() {
	logger := bark.NewLoggerFromLogrus(log.New())

	// Workflow execution parameters.
	workflowExecutionParameters := cadence.WorkerExecutionParameters{}
	workflowExecutionParameters.TaskListName = "exampleTaskList"
	workflowExecutionParameters.ConcurrentPollRoutineSize = 4

	// Launch worker.
	w.workflowWorker = cadence.NewWorkflowWorker(workflowExecutionParameters, workflowFactory, w.service, logger, nil /* reporter */, nil)
	w.workflowWorker.Start()
	log.Infoln("Started Deciders for workflows.")

	// Create activity execution parameters.
	activityExecutionParameters := cadence.WorkerExecutionParameters{}
	activityExecutionParameters.TaskListName = "exampleTaskList"
	activityExecutionParameters.ConcurrentPollRoutineSize = 10

	// Register activity instances and launch the worker.
	w.activityWorker = cadence.NewActivityWorker(activityExecutionParameters, activityFactory, w.service, logger, nil /* reporter */)
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
	workflowOptions := cadence.StartWorkflowOptions{
		WorkflowID:                             "examples-greetingWorkflow",
		WorkflowType:                           cadence.WorkflowType{Name: workflowName},
		TaskListName:                           "exampleTaskList",
		WorkflowInput:                          nil,
		ExecutionStartToCloseTimeoutSeconds:    10,
		DecisionTaskStartToCloseTimeoutSeconds: 10,
	}
	workflowClient := cadence.NewWorkflowClient(workflowOptions, w.service, nil /* reporter */)
	we, err := workflowClient.StartWorkflowExecution()
	if err != nil {
		log.Panicf("Failed to start workflow: %s, with error: %s.\n", workflowName, err.Error())
	}
	log.Infof("Created Workflow - workflow Id: %s, run Id: %s.\n", we.WorkflowID, we.RunID)
}
