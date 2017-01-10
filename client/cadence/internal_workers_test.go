package cadence

import (
	"testing"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/mocks"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/uber-common/bark"
)

type (
	WorkersTestSuite struct {
		suite.Suite
	}
)

// Test suite.
func (s *WorkersTestSuite) SetupTest() {
}

func TestWorkersTestSuite(t *testing.T) {
	formatter := &log.TextFormatter{}
	formatter.FullTimestamp = true
	log.SetFormatter(formatter)
	log.SetLevel(log.DebugLevel)

	suite.Run(t, new(WorkersTestSuite))
}

func (s *WorkersTestSuite) TestWorkflowWorker() {
	// mocks
	logger := bark.NewLoggerFromLogrus(log.New())
	service := new(mocks.TChanWorkflowService)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	executionParameters := WorkerExecutionParameters{TaskListName: "testTaskList", ConcurrentPollRoutineSize: 5}
	overides := &workerOverrides{workflowTaskHander: newSampleWorkflowTaskHandler(nil)}
	workflowWorker := newWorkflowWorkerInternal(executionParameters, testWorkflowDefinitionFactory, service, logger, nil, nil, overides)
	workflowWorker.Start()
	workflowWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorker() {
	// mocks
	logger := bark.NewLoggerFromLogrus(log.New())
	service := new(mocks.TChanWorkflowService)
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(&m.PollForActivityTaskResponse{}, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	executionParameters := WorkerExecutionParameters{TaskListName: "testTaskList", ConcurrentPollRoutineSize: 5}
	overides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler(nil)}
	activityWorker := newActivityWorkerInternal(executionParameters, []Activity{&greeterActivity{}}, service, logger, nil, overides)
	activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestPollForDecisionTask_InternalServiceError() {
	// mocks
	logger := bark.NewLoggerFromLogrus(log.New())
	service := new(mocks.TChanWorkflowService)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, &m.InternalServiceError{})

	executionParameters := WorkerExecutionParameters{TaskListName: "testDecisionTaskList", ConcurrentPollRoutineSize: 5}
	overides := &workerOverrides{workflowTaskHander: newSampleWorkflowTaskHandler(nil)}
	workflowWorker := newWorkflowWorkerInternal(executionParameters, testWorkflowDefinitionFactory, service, logger, nil, nil, overides)
	workflowWorker.Start()
	workflowWorker.Stop()
}
