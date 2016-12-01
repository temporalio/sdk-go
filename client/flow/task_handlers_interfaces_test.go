package flow

import (
	"testing"

	"golang.org/x/net/context"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/uber/tchannel-go/thrift"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/mocks"
)

type (
	PollLayerInterfacesTestSuite struct {
		suite.Suite
	}
)

// Workflow Context
type testWorkflowContext struct {
}

func (wc testWorkflowContext) WorkflowInfo() WorkflowInfo {
	return WorkflowInfo{}
}
func (wc testWorkflowContext) Complete(result []byte) {
}
func (wc testWorkflowContext) Fail(err error) {
}
func (wc testWorkflowContext) ScheduleActivityTask(parameters ExecuteActivityParameters, callback ResultHandler) {

}

// Activity Execution context
type testActivityExecutionContext struct {
}

func (ac testActivityExecutionContext) TaskToken() []byte {
	return []byte("")
}
func (ac testActivityExecutionContext) RecordActivityHeartbeat(details []byte) error {
	return nil
}

// Sample Workflow task handler
type sampleWorkflowTaskHandler struct {
	factory WorkflowDefinitionFactory
}

func (wth sampleWorkflowTaskHandler) ProcessWorkflowTask(workflowTask *WorkflowTask) (*m.RespondDecisionTaskCompletedRequest, error) {
	return &m.RespondDecisionTaskCompletedRequest{
		TaskToken: workflowTask.task.TaskToken,
	}, nil
}

func (wth sampleWorkflowTaskHandler) LoadWorkflowThroughReplay(workflowTask *WorkflowTask) (WorkflowDefinition, error) {
	return &helloWorldWorkflow{}, nil
}

func newSampleWorkflowTaskHandler(factory WorkflowDefinitionFactory) *sampleWorkflowTaskHandler {
	return &sampleWorkflowTaskHandler{factory: factory}
}

// Sample ActivityTaskHandler
type sampleActivityTaskHandler struct {
	activityRegistry map[m.ActivityType]*ActivityImplementation
}

func newSampleActivityTaskHandler(activityRegistry map[m.ActivityType]*ActivityImplementation) *sampleActivityTaskHandler {
	return &sampleActivityTaskHandler{activityRegistry: activityRegistry}
}

func (ath sampleActivityTaskHandler) Execute(context context.Context, activityTask *ActivityTask) (interface{}, error) {
	//activityImplementation := *ath.activityRegistry[*activityTask.task.ActivityType]
	activityImplementation := &greeterActivity{}
	activityContext := &testActivityExecutionContext{}
	result, err := activityImplementation.Execute(activityContext, activityTask.task.Input)
	if err != nil {
		reason := err.Error()
		return &m.RespondActivityTaskFailedRequest{
			TaskToken: activityTask.task.TaskToken,
			Reason:    &reason,
		}, nil
	}
	return &m.RespondActivityTaskCompletedRequest{
		TaskToken: activityTask.task.TaskToken,
		Result_:   result,
	}, nil
}

// Test suite.
func (s *PollLayerInterfacesTestSuite) SetupTest() {
}

func TestPollLayerInterfacesTestSuite(t *testing.T) {
	suite.Run(t, new(PollLayerInterfacesTestSuite))
}

func (s *PollLayerInterfacesTestSuite) TestProcessWorkflowTaskInterface() {
	// Create service endpoint and get a workflow task.
	service := new(mocks.TChanWorkflowService)
	ctx, _ := thrift.NewContext(10)

	// mocks
	service.On("PollForDecisionTask", mock.Anything, mock.Anything).Return(&m.PollForDecisionTaskResponse{}, nil)
	service.On("RespondDecisionTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	response, err := service.PollForDecisionTask(ctx, &m.PollForDecisionTaskRequest{})
	s.NoError(err)

	// Process task and respond to the service.
	workflowTaskHandler := newSampleWorkflowTaskHandler(testWorkflowDefinitionFactory)
	completionRequest, err := workflowTaskHandler.ProcessWorkflowTask(&WorkflowTask{response})
	s.NoError(err)

	err = service.RespondDecisionTaskCompleted(ctx, completionRequest)
	s.NoError(err)
}

func (s *PollLayerInterfacesTestSuite) TestProcessActivityTaskInterface() {
	// Create service endpoint and get a activity task.
	service := new(mocks.TChanWorkflowService)
	ctx, _ := thrift.NewContext(10)

	// mocks
	service.On("PollForActivityTask", mock.Anything, mock.Anything).Return(&m.PollForActivityTaskResponse{}, nil)
	service.On("RespondActivityTaskCompleted", mock.Anything, mock.Anything).Return(nil)

	response, err := service.PollForActivityTask(ctx, &m.PollForActivityTaskRequest{})
	s.NoError(err)

	// Execute activity task and respond to the service.
	activationRegistry := make(map[m.ActivityType]*ActivityImplementation)
	activityTaskHandler := newSampleActivityTaskHandler(activationRegistry)
	request, err := activityTaskHandler.Execute(nil, &ActivityTask{response})
	s.NoError(err)
	switch request.(type) {
	case m.RespondActivityTaskCompletedRequest:
		err = service.RespondActivityTaskCompleted(ctx, request.(*m.RespondActivityTaskCompletedRequest))
		s.NoError(err)
	case m.RespondActivityTaskFailedRequest:
		err = service.RespondActivityTaskFailed(ctx, request.(*m.RespondActivityTaskFailedRequest))
		s.NoError(err)
	}
}
