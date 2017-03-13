package examples

import (
	"encoding/json"
	"errors"

	log "github.com/Sirupsen/logrus"
	"github.com/uber-common/bark"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
)

type (
	// WorkflowHelper class for workflow helpers.
	WorkflowHelper struct {
		service        m.TChanWorkflowService
		workflowWorker cadence.Lifecycle
		activityWorker cadence.Lifecycle
	}
)

var workflowFactory = func(wt cadence.WorkflowType) (cadence.Workflow, error) {
	switch wt.Name {
	case "greetingsWorkflow":
		return greetingsWorkflow{}, nil
	}
	return nil, errors.New("Invalid workflow type")
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
	workflowExecutionParameters := cadence.WorkerExecutionParameters{
		TaskList:                  "exampleTaskList",
		ConcurrentPollRoutineSize: 4,
		Logger: logger,
	}

	// Launch worker.
	w.workflowWorker = cadence.NewWorkflowWorker(workflowFactory, w.service, workflowExecutionParameters)
	w.workflowWorker.Start()
	log.Infoln("Started Deciders for workflows.")

	// Create activity execution parameters.
	activityExecutionParameters := cadence.WorkerExecutionParameters{
		TaskList:                  "exampleTaskList",
		ConcurrentPollRoutineSize: 10,
		Logger: logger,
	}

	// Register activity instances and launch the worker.
	activities := []cadence.Activity{&getNameActivity{}, &getGreetingActivity{}, &sayGreetingActivity{}}
	w.activityWorker = cadence.NewActivityWorker(activities, w.service, activityExecutionParameters)
	w.activityWorker.Start()
	log.Infoln("Started activities for workflows.")
}

// StopWorkers stops necessary workers.
func (w *WorkflowHelper) StopWorkers() {
	if w.workflowWorker != nil {
		w.workflowWorker.Stop()
	}

	if w.activityWorker != nil {
		w.activityWorker.Stop()
	}
}

// StartWorkflow starts an workflow instance.
func (w *WorkflowHelper) StartWorkflow(workflowName string) {
	workflowOptions := cadence.StartWorkflowOptions{
		ID:       "examples-greetingWorkflow",
		Type:     cadence.WorkflowType{Name: workflowName},
		TaskList: "exampleTaskList",
		Input:    nil,
		ExecutionStartToCloseTimeoutSeconds:    10,
		DecisionTaskStartToCloseTimeoutSeconds: 10,
	}
	workflowClient := cadence.NewWorkflowClient(w.service, nil /* reporter */)
	we, err := workflowClient.StartWorkflowExecution(workflowOptions)
	if err != nil {
		log.Panicf("Failed to start workflow: %s, with error: %s.\n", workflowName, err.Error())
	}
	log.Infof("Created Workflow - workflow Id: %s, run Id: %s.\n", we.ID, we.RunID)
}
