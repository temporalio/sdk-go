// Copyright (c) 2018 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
package internal

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"

	"github.com/golang/mock/gomock"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/internal/common/metrics"
)

const (
	domain                = "some random domain"
	workflowID            = "some random workflow ID"
	workflowType          = "some random workflow type"
	runID                 = "some random run ID"
	tasklist              = "some random tasklist"
	identity              = "some random identity"
	timeoutInSeconds      = 20
	workflowIDReusePolicy = WorkflowIDReusePolicyAllowDuplicateFailedOnly
)

// historyEventIteratorSuite

type (
	historyEventIteratorSuite struct {
		suite.Suite
		mockCtrl              *gomock.Controller
		workflowServiceClient *workflowservicetest.MockClient
		historyEventIterator  HistoryEventIterator
	}
)

func TestHistoryEventIteratorSuite(t *testing.T) {
	s := new(historyEventIteratorSuite)
	suite.Run(t, s)
}

func (s *historyEventIteratorSuite) SetupSuite() {
	if testing.Verbose() {
		log.SetOutput(os.Stdout)
	}
}

func (s *historyEventIteratorSuite) SetupTest() {
	// Create service endpoint
	s.mockCtrl = gomock.NewController(s.T())
	s.workflowServiceClient = workflowservicetest.NewMockClient(s.mockCtrl)

	paginate := func(nexttoken []byte) (*shared.GetWorkflowExecutionHistoryResponse, error) {
		filterType := shared.HistoryEventFilterTypeAllEvent
		request := getGetWorkflowExecutionHistoryRequest(filterType)
		request.NextPageToken = nexttoken
		return s.workflowServiceClient.GetWorkflowExecutionHistory(context.Background(), request)
	}

	s.historyEventIterator = &historyEventIteratorImpl{
		paginate: paginate,
	}
}

func (s *historyEventIteratorSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *historyEventIteratorSuite) TestIterator_NoError() {
	filterType := shared.HistoryEventFilterTypeAllEvent
	request1 := getGetWorkflowExecutionHistoryRequest(filterType)
	response1 := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				// dummy history event
				&shared.HistoryEvent{},
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getGetWorkflowExecutionHistoryRequest(filterType)
	request2.NextPageToken = response1.NextPageToken
	response2 := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				// dummy history event
				&shared.HistoryEvent{},
			},
		},
		NextPageToken: nil,
	}

	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request1).Return(response1, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request2).Return(response2, nil).Times(1)

	events := []*shared.HistoryEvent{}
	for s.historyEventIterator.HasNext() {
		event, err := s.historyEventIterator.Next()
		s.Nil(err)
		events = append(events, event)
	}
	s.Equal(2, len(events))
}

func (s *historyEventIteratorSuite) TestIterator_Error() {
	filterType := shared.HistoryEventFilterTypeAllEvent
	request1 := getGetWorkflowExecutionHistoryRequest(filterType)
	response1 := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				// dummy history event
				&shared.HistoryEvent{},
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getGetWorkflowExecutionHistoryRequest(filterType)
	request2.NextPageToken = response1.NextPageToken

	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request1).Return(response1, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request2).Return(nil, errors.New("some random err")).Times(1)

	s.True(s.historyEventIterator.HasNext())
	event, err := s.historyEventIterator.Next()
	s.NotNil(event)
	s.Nil(err)

	s.True(s.historyEventIterator.HasNext())
	event, err = s.historyEventIterator.Next()
	s.Nil(event)
	s.NotNil(err)
}

// workflowRunSuite

type (
	workflowRunSuite struct {
		suite.Suite
		mockCtrl              *gomock.Controller
		workflowServiceClient *workflowservicetest.MockClient
		workflowClient        Client
	}
)

func TestWorkflowRunSuite(t *testing.T) {
	s := new(workflowRunSuite)
	suite.Run(t, s)
}

func (s *workflowRunSuite) SetupSuite() {
	if testing.Verbose() {
		log.SetOutput(os.Stdout)
	}
}

func (s *workflowRunSuite) TearDownSuite() {

}

func (s *workflowRunSuite) SetupTest() {
	// Create service endpoint
	s.mockCtrl = gomock.NewController(s.T())
	s.workflowServiceClient = workflowservicetest.NewMockClient(s.mockCtrl)

	metricsScope := metrics.NewTaggedScope(nil)
	options := &ClientOptions{
		MetricsScope: metricsScope,
		Identity:     identity,
	}
	s.workflowClient = NewClient(s.workflowServiceClient, domain, options)
}

func (s *workflowRunSuite) TearDownTest() {
	s.mockCtrl.Finish()
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Success() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := shared.HistoryEventFilterTypeCloseEvent
	eventType := shared.EventTypeWorkflowExecutionCompleted
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(newDefaultDataConverter(), workflowResult)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType,
					WorkflowExecutionCompletedEventAttributes: &shared.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflowWorkflowExecutionAlreadyStartedError() {
	alreadyStartedErr := &shared.WorkflowExecutionAlreadyStartedError{
		RunId:          common.StringPtr(runID),
		Message:        common.StringPtr("Already Started"),
		StartRequestId: common.StringPtr(uuid.NewUUID().String()),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, alreadyStartedErr).Times(1)

	eventType := shared.EventTypeWorkflowExecutionCompleted
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(nil, workflowResult)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				{
					EventType: &eventType,
					WorkflowExecutionCompletedEventAttributes: &shared.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	getHistory := s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(getResponse, nil).Times(1)
	getHistory.Do(func(ctx interface{}, getRequest *shared.GetWorkflowExecutionHistoryRequest, opt1 interface{}, opt2 interface{}, opt3 interface{}) {
		workflowID := getRequest.Execution.WorkflowId
		s.NotNil(workflowID)
		s.NotEmpty(*workflowID)
	})

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

// Test for the bug in ExecuteWorkflow.
// When Options.ID was empty then GetWorkflowExecutionHistory was called with an empty WorkflowID.
func (s *workflowRunSuite) TestExecuteWorkflow_NoIdInOptions() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	eventType := shared.EventTypeWorkflowExecutionCompleted
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(nil, workflowResult)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				{
					EventType: &eventType,
					WorkflowExecutionCompletedEventAttributes: &shared.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	getHistory := s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)
	getHistory.Do(func(ctx interface{}, getRequest *shared.GetWorkflowExecutionHistoryRequest, opt1 interface{}, opt2 interface{}, opt3 interface{}) {
		workflowID := getRequest.Execution.WorkflowId
		s.NotNil(workflowID)
		s.NotEmpty(*workflowID)
	})

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Cancelled() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := shared.HistoryEventFilterTypeCloseEvent
	eventType := shared.EventTypeWorkflowExecutionCanceled
	details := "some details"
	encodedDetails, _ := encodeArg(newDefaultDataConverter(), details)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType,
					WorkflowExecutionCanceledEventAttributes: &shared.WorkflowExecutionCanceledEventAttributes{
						Details: encodedDetails,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.NotNil(err)
	_, ok := err.(*CanceledError)
	s.True(ok)
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Failed() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := shared.HistoryEventFilterTypeCloseEvent
	eventType := shared.EventTypeWorkflowExecutionFailed
	reason := "some reason"
	details := "some details"
	dataConverter := newDefaultDataConverter()
	encodedDetails, _ := encodeArg(dataConverter, details)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType,
					WorkflowExecutionFailedEventAttributes: &shared.WorkflowExecutionFailedEventAttributes{
						Reason:  common.StringPtr(reason),
						Details: encodedDetails,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Equal(constructError(reason, encodedDetails, dataConverter), err)
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Terminated() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := shared.HistoryEventFilterTypeCloseEvent
	eventType := shared.EventTypeWorkflowExecutionTerminated
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType,
					WorkflowExecutionTerminatedEventAttributes: &shared.WorkflowExecutionTerminatedEventAttributes{},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Equal(newTerminatedError(), err)
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_TimedOut() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := shared.HistoryEventFilterTypeCloseEvent
	eventType := shared.EventTypeWorkflowExecutionTimedOut
	timeType := shared.TimeoutTypeScheduleToStart
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType,
					WorkflowExecutionTimedOutEventAttributes: &shared.WorkflowExecutionTimedOutEventAttributes{
						TimeoutType: &timeType,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.NotNil(err)
	_, ok := err.(*TimeoutError)
	s.True(ok)
	s.Equal(timeType, err.(*TimeoutError).TimeoutType())
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_ContinueAsNew() {
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	newRunID := "some other random run ID"
	filterType := shared.HistoryEventFilterTypeCloseEvent
	eventType1 := shared.EventTypeWorkflowExecutionContinuedAsNew
	getRequest1 := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse1 := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType1,
					WorkflowExecutionContinuedAsNewEventAttributes: &shared.WorkflowExecutionContinuedAsNewEventAttributes{
						NewExecutionRunId: common.StringPtr(newRunID),
					},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest1, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse1, nil).Times(1)

	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(newDefaultDataConverter(), workflowResult)
	eventType2 := shared.EventTypeWorkflowExecutionCompleted
	getRequest2 := getGetWorkflowExecutionHistoryRequest(filterType)
	getRequest2.Execution.RunId = common.StringPtr(newRunID)
	getResponse2 := &shared.GetWorkflowExecutionHistoryResponse{
		History: &shared.History{
			Events: []*shared.HistoryEvent{
				&shared.HistoryEvent{
					EventType: &eventType2,
					WorkflowExecutionCompletedEventAttributes: &shared.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest2, gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse2, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                              workflowID,
			TaskList:                        tasklist,
			ExecutionStartToCloseTimeout:    timeoutInSeconds * time.Second,
			DecisionTaskStartToCloseTimeout: timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:           workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

func getGetWorkflowExecutionHistoryRequest(filterType shared.HistoryEventFilterType) *shared.GetWorkflowExecutionHistoryRequest {
	isLongPoll := true

	request := &shared.GetWorkflowExecutionHistoryRequest{
		Domain: common.StringPtr(domain),
		Execution: &shared.WorkflowExecution{
			WorkflowId: common.StringPtr(workflowID),
			RunId:      getRunID(runID),
		},
		WaitForNewEvent:        common.BoolPtr(isLongPoll),
		HistoryEventFilterType: &filterType,
	}

	return request
}

// workflow client test suite
type (
	workflowClientTestSuite struct {
		suite.Suite
		mockCtrl *gomock.Controller
		service  *workflowservicetest.MockClient
		client   Client
	}
)

func TestWorkflowClientSuite(t *testing.T) {
	suite.Run(t, new(workflowClientTestSuite))
}

func (s *workflowClientTestSuite) SetupSuite() {
	if testing.Verbose() {
		log.SetOutput(os.Stdout)
	}
}

func (s *workflowClientTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicetest.NewMockClient(s.mockCtrl)
	s.client = NewClient(s.service, domain, nil)
}

func (s *workflowClientTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *workflowClientTestSuite) TestSignalWithStartWorkflow() {
	signalName := "my signal"
	signalInput := []byte("my signal input")
	options := StartWorkflowOptions{
		ID:                              workflowID,
		TaskList:                        tasklist,
		ExecutionStartToCloseTimeout:    timeoutInSeconds,
		DecisionTaskStartToCloseTimeout: timeoutInSeconds,
	}

	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.service.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(2)

	resp, err := s.client.SignalWithStartWorkflow(context.Background(), workflowID, signalName, signalInput,
		options, workflowType)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.RunID)

	resp, err = s.client.SignalWithStartWorkflow(context.Background(), "", signalName, signalInput,
		options, workflowType)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.RunID)
}

func (s *workflowClientTestSuite) TestSignalWithStartWorkflow_Error() {
	signalName := "my signal"
	signalInput := []byte("my signal input")
	options := StartWorkflowOptions{}

	resp, err := s.client.SignalWithStartWorkflow(context.Background(), workflowID, signalName, signalInput,
		options, workflowType)
	s.Equal(errors.New("missing TaskList"), err)
	s.Nil(resp)

	options.TaskList = tasklist
	resp, err = s.client.SignalWithStartWorkflow(context.Background(), workflowID, signalName, signalInput,
		options, workflowType)
	s.NotNil(err)
	s.Nil(resp)

	options.ExecutionStartToCloseTimeout = timeoutInSeconds
	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.service.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil)
	resp, err = s.client.SignalWithStartWorkflow(context.Background(), workflowID, signalName, signalInput,
		options, workflowType)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.RunID)
}

func (s *workflowClientTestSuite) TestStartWorkflow() {
	client, ok := s.client.(*workflowClient)
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                              workflowID,
		TaskList:                        tasklist,
		ExecutionStartToCloseTimeout:    timeoutInSeconds,
		DecisionTaskStartToCloseTimeout: timeoutInSeconds,
	}
	f1 := func(ctx Context, r []byte) string {
		return "result"
	}

	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil)

	resp, err := client.StartWorkflow(context.Background(), options, f1, []byte("test"))
	s.Equal(newDefaultDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.RunID)
}

func (s *workflowClientTestSuite) TestStartWorkflow_WithDataConverter() {
	dc := newTestDataConverter()
	s.client = NewClient(s.service, domain, &ClientOptions{DataConverter: dc})
	client, ok := s.client.(*workflowClient)
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                              workflowID,
		TaskList:                        tasklist,
		ExecutionStartToCloseTimeout:    timeoutInSeconds,
		DecisionTaskStartToCloseTimeout: timeoutInSeconds,
	}
	f1 := func(ctx Context, r []byte) string {
		return "result"
	}
	input := []byte("test")

	createResponse := &shared.StartWorkflowExecutionResponse{
		RunId: common.StringPtr(runID),
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).
		Do(func(_ interface{}, req *shared.StartWorkflowExecutionRequest, _ ...interface{}) {
			dc := client.dataConverter
			encodedArg, _ := dc.ToData(input)
			s.Equal(req.Input, encodedArg)
			var decodedArg []byte
			dc.FromData(req.Input, &decodedArg)
			s.Equal(input, decodedArg)
		})

	resp, err := client.StartWorkflow(context.Background(), options, f1, input)
	s.Equal(newTestDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.RunID)
}
