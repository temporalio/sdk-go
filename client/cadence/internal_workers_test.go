package cadence

import (
	"testing"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	m "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/mocks"
	"go.uber.org/zap"
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
	domain := "testDomain"
	// mocks
	logger, _ := zap.NewDevelopment()
	service := new(mocks.TChanWorkflowService)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger: logger,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler(nil)}
	workflowWorker := newWorkflowWorkerInternal(testWorkflowDefinitionFactory, service, domain, executionParameters, nil, overrides)
	workflowWorker.Start()
	workflowWorker.Stop()
}

func (s *WorkersTestSuite) TestActivityWorker() {
	domain := "testDomain"
	// mocks
	logger, _ := zap.NewDevelopment()
	service := new(mocks.TChanWorkflowService)
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(&m.PollForActivityTaskResponse{}, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	executionParameters := workerExecutionParameters{
		TaskList:                  "testTaskList",
		ConcurrentPollRoutineSize: 5,
		Logger: logger,
	}
	overrides := &workerOverrides{activityTaskHandler: newSampleActivityTaskHandler(nil)}
	activityWorker := newActivityWorker([]activity{&greeterActivity{}}, service, domain, executionParameters, overrides)
	activityWorker.Start()
	activityWorker.Stop()
}

func (s *WorkersTestSuite) TestPollForDecisionTask_InternalServiceError() {
	domain := "testDomain"
	// mocks
	service := new(mocks.TChanWorkflowService)
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, &m.InternalServiceError{})

	executionParameters := workerExecutionParameters{
		TaskList:                  "testDecisionTaskList",
		ConcurrentPollRoutineSize: 5,
	}
	overrides := &workerOverrides{workflowTaskHandler: newSampleWorkflowTaskHandler(nil)}
	workflowWorker := newWorkflowWorkerInternal(testWorkflowDefinitionFactory, service, domain, executionParameters, nil, overrides)
	workflowWorker.Start()
	workflowWorker.Stop()
}
