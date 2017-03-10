package samples

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"github.com/Sirupsen/logrus"
	"github.com/pborman/uuid"
	"github.com/uber-common/bark"
	"github.com/uber-go/tally"
	"github.com/uber/tchannel-go"
	"github.com/uber/tchannel-go/thrift"
)

type (
	// SampleHelper class for workflow sample helper.
	SampleHelper struct {
		service        m.TChanWorkflowService
		workflowWorker cadence.Lifecycle
		activityWorker cadence.Lifecycle
		hostPort       string
		serviceName    string
		scope          tally.Scope
		logger         bark.Logger
		ResultCh       chan []byte
	}
)

// SetupConfig setup the config for the sample code run
func (h *SampleHelper) SetupConfig() {
	h.hostPort = "127.0.0.1:7933" // pointing to cadence server:port
	h.serviceName = "cadence-frontend"

	ch, err := tchannel.NewChannel("cadence-client-sample-app", nil)
	if err != nil {
		logrus.Panic("Failed to get a client: %s\n", err.Error())
	}
	opts := &thrift.ClientOptions{HostPort: h.hostPort}

	tclient := thrift.NewClient(ch, h.serviceName, opts)
	h.service = m.NewTChanWorkflowServiceClient(tclient)
	h.logger = bark.NewLoggerFromLogrus(logrus.New())
	h.scope = tally.NewTestScope("sample", map[string]string{})
	h.ResultCh = make(chan []byte)
}

// StartWorkflowWorker starts workflow worker
func (h *SampleHelper) StartWorkflowWorker(taskListName string, pollSize int, workflowFactory cadence.WorkflowFactory) {
	// Workflow execution parameters.
	workflowExecutionParameters := cadence.WorkerExecutionParameters{}
	workflowExecutionParameters.TaskList = taskListName
	workflowExecutionParameters.ConcurrentPollRoutineSize = pollSize
	workflowExecutionParameters.Logger = h.logger

	// Launch worker.
	h.workflowWorker = cadence.NewWorkflowWorker(workflowFactory, h.service, workflowExecutionParameters)
	h.workflowWorker.Start()
	logrus.Infoln("Started Deciders for workflows.")
}

// StartActivityWorker starts activities worker
func (h *SampleHelper) StartActivityWorker(taskListName string, pollSize int, activities []cadence.Activity) {
	// Create activity execution parameters.
	activityExecutionParameters := cadence.WorkerExecutionParameters{}
	activityExecutionParameters.TaskList = taskListName
	activityExecutionParameters.ConcurrentPollRoutineSize = pollSize
	activityExecutionParameters.Logger = h.logger

	// Register activity instances and launch the worker.
	h.activityWorker = cadence.NewActivityWorker(activities, h.service, activityExecutionParameters)
	h.activityWorker.Start()
	logrus.Infoln("Started activities for workflows.")
}

// StartWorkflow starts a new workflow
func (h *SampleHelper) StartWorkflow(workflowName, workflowTasklistName string, input []byte, timeoutSeconds int32) {
	workflowID := uuid.New()
	workflowOptions := cadence.StartWorkflowOptions{
		ID:       workflowID,
		Type:     cadence.WorkflowType{Name: workflowName},
		TaskList: workflowTasklistName,
		Input:    input,
		ExecutionStartToCloseTimeoutSeconds:    timeoutSeconds,
		DecisionTaskStartToCloseTimeoutSeconds: timeoutSeconds,
	}
	workflowClient := cadence.NewWorkflowClient(h.service, h.scope)
	we, err := workflowClient.StartWorkflowExecution(workflowOptions)
	if err != nil {
		logrus.Errorf("Failed to create workflow with error: %+v", err)
		panic("Failed to create workflow.")
	} else {
		logrus.Infof("Start Workflow Id: %s, run Id: %s \n", workflowID, we.RunID)
	}
}

// ActivityParameters create cadence.ExecuteActivityParameters
func ActivityParameters(tasklistName, activityName string, input []byte) cadence.ExecuteActivityParameters {
	return cadence.ExecuteActivityParameters{
		TaskListName: tasklistName,
		ActivityType: cadence.ActivityType{Name: activityName},
		Input:        input,
		ScheduleToCloseTimeoutSeconds: 60,
		ScheduleToStartTimeoutSeconds: 60,
		StartToCloseTimeoutSeconds:    60,
		HeartbeatTimeoutSeconds:       20,
	}
}
