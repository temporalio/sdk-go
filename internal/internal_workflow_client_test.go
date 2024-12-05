// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	updatepb "go.temporal.io/api/update/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	ilog "go.temporal.io/sdk/internal/log"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/serializer"
	iconverter "go.temporal.io/sdk/internal/converter"
)

const (
	workflowID            = "some random workflow ID"
	workflowType          = "some random workflow type"
	runID                 = "some random run ID"
	taskqueue             = "some random taskqueue"
	identity              = "some random identity"
	timeoutInSeconds      = 20
	workflowIDReusePolicy = enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
	testHeader            = "test-header"
)

// historyEventIteratorSuite

type (
	historyEventIteratorSuite struct {
		suite.Suite
		mockCtrl              *gomock.Controller
		workflowServiceClient *workflowservicemock.MockWorkflowServiceClient
		wfClient              *WorkflowClient
	}
)

// keysPropagator propagates the list of keys across a workflow,
// interpreting the payloads as strings.
type keysPropagator struct {
	keys []string
}

// NewKeysPropagator returns a context propagator that propagates a set of
// string key-value pairs across a workflow
func NewKeysPropagator(keys []string) ContextPropagator {
	return &keysPropagator{keys}
}

// Inject injects values from context into headers for propagation
func (s *keysPropagator) Inject(ctx context.Context, writer HeaderWriter) error {
	for _, key := range s.keys {
		value, ok := ctx.Value(contextKey(key)).(string)
		if !ok {
			return fmt.Errorf("unable to extract key from context %v", key)
		}
		encodedValue, err := converter.GetDefaultDataConverter().ToPayload(value)
		if err != nil {
			return err
		}
		writer.Set(key, encodedValue)
	}
	return nil
}

// InjectFromWorkflow injects values from context into headers for propagation
func (s *keysPropagator) InjectFromWorkflow(ctx Context, writer HeaderWriter) error {
	for _, key := range s.keys {
		value, ok := ctx.Value(contextKey(key)).(string)
		if !ok {
			return fmt.Errorf("unable to extract key from context %v", key)
		}
		encodedValue, err := converter.GetDefaultDataConverter().ToPayload(value)
		if err != nil {
			return err
		}
		writer.Set(key, encodedValue)
	}
	return nil
}

// Extract extracts values from headers and puts them into context
func (s *keysPropagator) Extract(ctx context.Context, reader HeaderReader) (context.Context, error) {
	for _, key := range s.keys {
		value, ok := reader.Get(key)
		if !ok {
			// If key that should be propagated doesn't exist in the header, ignore the key.
			continue
		}
		var decodedValue string
		err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
		if err != nil {
			return ctx, err
		}
		ctx = context.WithValue(ctx, contextKey(key), decodedValue)
	}
	return ctx, nil
}

// ExtractToWorkflow extracts values from headers and puts them into context
func (s *keysPropagator) ExtractToWorkflow(ctx Context, reader HeaderReader) (Context, error) {
	for _, key := range s.keys {
		value, ok := reader.Get(key)
		if !ok {
			// If key that should be propagated doesn't exist in the header, ignore the key.
			continue
		}
		var decodedValue string
		err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
		if err != nil {
			return ctx, err
		}
		ctx = WithValue(ctx, contextKey(key), decodedValue)
	}
	return ctx, nil
}

func TestHistoryEventIteratorSuite(t *testing.T) {
	s := new(historyEventIteratorSuite)
	suite.Run(t, s)
}

func (s *historyEventIteratorSuite) SetupTest() {
	// Create service endpoint
	s.mockCtrl = gomock.NewController(s.T())
	s.workflowServiceClient = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	s.workflowServiceClient.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

	s.wfClient = &WorkflowClient{
		workflowService:          s.workflowServiceClient,
		namespace:                DefaultNamespace,
		excludeInternalFromRetry: &atomic.Bool{},
	}
}

func (s *historyEventIteratorSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *historyEventIteratorSuite) TestIterator_NoError() {
	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT
	request1 := getGetWorkflowExecutionHistoryRequest(filterType)
	response1 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				// dummy history event
				{},
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getGetWorkflowExecutionHistoryRequest(filterType)
	request2.NextPageToken = response1.NextPageToken
	response2 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				// dummy history event
				{},
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	dummyEvent := []*historypb.HistoryEvent{
		// dummy history event
		{},
	}

	blobData := serializeEvents(dummyEvent)
	request3 := getGetWorkflowExecutionHistoryRequest(filterType)
	request3.NextPageToken = response2.NextPageToken
	response3 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		RawHistory: []*commonpb.DataBlob{
			// dummy history event
			blobData,
		},
		NextPageToken: nil,
	}

	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request2, gomock.Any()).Return(response2, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request3, gomock.Any()).Return(response3, nil).Times(1)

	var events []*historypb.HistoryEvent
	iter := s.wfClient.GetWorkflowHistory(context.Background(), workflowID, runID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		s.Nil(err)
		events = append(events, event)
	}
	s.Equal(3, len(events))
}

func (s *historyEventIteratorSuite) TestIterator_NoError_EmptyPage() {
	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT
	request1 := getGetWorkflowExecutionHistoryRequest(filterType)
	response1 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getGetWorkflowExecutionHistoryRequest(filterType)
	request2.NextPageToken = response1.NextPageToken
	response2 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				// dummy history event
				{},
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	dummyEvent := []*historypb.HistoryEvent{
		// dummy history event
		{},
	}

	blobData := serializeEvents(dummyEvent)
	request3 := getGetWorkflowExecutionHistoryRequest(filterType)
	request3.NextPageToken = response2.NextPageToken
	response3 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		RawHistory: []*commonpb.DataBlob{
			// dummy history event
			blobData,
		},
		NextPageToken: nil,
	}

	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request2, gomock.Any()).Return(response2, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request3, gomock.Any()).Return(response3, nil).Times(1)

	var events []*historypb.HistoryEvent
	iter := s.wfClient.GetWorkflowHistory(context.Background(), workflowID, runID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		s.Nil(err)
		events = append(events, event)
	}
	s.Equal(2, len(events))
}

func (s *historyEventIteratorSuite) TestIteratorError() {
	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT
	request1 := getGetWorkflowExecutionHistoryRequest(filterType)
	response1 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				// dummy history event
				{},
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getGetWorkflowExecutionHistoryRequest(filterType)
	request2.NextPageToken = response1.NextPageToken

	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)

	iter := s.wfClient.GetWorkflowHistory(context.Background(), workflowID, runID, true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)

	s.True(iter.HasNext())
	event, err := iter.Next()
	s.NotNil(event)
	s.Nil(err)

	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), request2, gomock.Any()).Return(nil, serviceerror.NewNotFound("")).Times(1)

	s.True(iter.HasNext())
	event, err = iter.Next()
	s.Nil(event)
	s.NotNil(err)
}

// workflowRunSuite

type (
	workflowRunSuite struct {
		suite.Suite
		mockCtrl              *gomock.Controller
		workflowServiceClient *workflowservicemock.MockWorkflowServiceClient
		workflowClient        Client
		dataConverter         converter.DataConverter
	}
)

func TestWorkflowRunSuite(t *testing.T) {
	s := new(workflowRunSuite)
	suite.Run(t, s)
}

func (s *workflowRunSuite) TearDownSuite() {

}

func (s *workflowRunSuite) SetupTest() {
	// Create service endpoint
	s.mockCtrl = gomock.NewController(s.T())
	s.workflowServiceClient = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	s.workflowServiceClient.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

	options := ClientOptions{
		MetricsHandler: metrics.NopHandler,
		Identity:       identity,
		Logger:         ilog.NewNopLogger(),
	}
	s.workflowClient = NewServiceClient(s.workflowServiceClient, nil, options)
	s.dataConverter = converter.GetDefaultDataConverter()
}

func (s *workflowRunSuite) TearDownTest() {
	s.mockCtrl.Finish()
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Success() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(converter.GetDefaultDataConverter(), workflowResult)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_RawHistory_Success() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(converter.GetDefaultDataConverter(), workflowResult)
	events := []*historypb.HistoryEvent{
		{
			EventType: eventType,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
				Result: encodedResult,
			}},
		},
	}

	blobData := serializeEvents(events)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		RawHistory: []*commonpb.DataBlob{
			blobData,
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                    workflowID,
			TaskQueue:             taskqueue,
			WorkflowRunTimeout:    timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:   timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy: workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflowWorkflowExecutionAlreadyStartedError() {
	mockerr := serviceerror.NewWorkflowExecutionAlreadyStarted("Already Started", "", runID)
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, mockerr).Times(1)

	_, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                                       workflowID,
			TaskQueue:                                taskqueue,
			WorkflowExecutionTimeout:                 timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:                      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:                    workflowIDReusePolicy,
			WorkflowExecutionErrorWhenAlreadyStarted: true,
		}, workflowType,
	)
	s.Error(err)
	s.Equal(mockerr, err)
}

func (s *workflowRunSuite) TestExecuteWorkflowWorkflowExecutionAlreadyStartedErrorAllowStarted() {
	s.alreadyStartedErrTest(s.dataConverter, false)
}

func (s *workflowRunSuite) TestExecuteWorkflowWorkflowExecutionAlreadyStartedError_RawHistory() {
	s.alreadyStartedErrTest(converter.GetDefaultDataConverter(), true)
}

func (s *workflowRunSuite) alreadyStartedErrTest(dc converter.DataConverter, rawHistory bool) {
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, serviceerror.NewWorkflowExecutionAlreadyStarted("Already Started", "", runID)).Times(1)

	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(dc, workflowResult)
	var getResponse *workflowservice.GetWorkflowExecutionHistoryResponse
	if rawHistory {
		events := []*historypb.HistoryEvent{
			{
				EventType: eventType,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
					Result: encodedResult,
				}},
			},
		}

		blobData := serializeEvents(events)

		getResponse = &workflowservice.GetWorkflowExecutionHistoryResponse{
			RawHistory: []*commonpb.DataBlob{
				blobData,
			},
			NextPageToken: nil,
		}
	} else {
		getResponse = &workflowservice.GetWorkflowExecutionHistoryResponse{
			History: &historypb.History{
				Events: []*historypb.HistoryEvent{
					{
						EventType: eventType,
						Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
							Result: encodedResult,
						}},
					},
				},
			},
			NextPageToken: nil,
		}
	}

	getHistory := s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(getResponse, nil).Times(1)
	getHistory.Do(func(ctx interface{}, getRequest *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) {
		workflowID := getRequest.Execution.WorkflowId
		s.NotNil(workflowID)
		s.NotEmpty(workflowID)
	})

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                    workflowID,
			TaskQueue:             taskqueue,
			WorkflowRunTimeout:    timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:   timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy: workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

// Test for the bug in ExecuteWorkflow.
// When Options.ID was empty then GetWorkflowExecutionHistory was called with an empty WorkflowID.
func (s *workflowRunSuite) TestExecuteWorkflow_NoIdInOptions() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(s.dataConverter, workflowResult)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	var wid string
	getHistory := s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)
	getHistory.Do(func(ctx interface{}, getRequest *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) {
		wid = getRequest.Execution.WorkflowId
		s.NotEmpty(wid)
	})

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
	s.Equal(workflowRun.GetID(), wid)
}

// Test for the bug in ExecuteWorkflow in the case of raw history returned from API.
// When Options.ID was empty then GetWorkflowExecutionHistory was called with an empty WorkflowID.
func (s *workflowRunSuite) TestExecuteWorkflow_NoIdInOptions_RawHistory() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(s.dataConverter, workflowResult)
	events := []*historypb.HistoryEvent{
		{
			EventType: eventType,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
				Result: encodedResult,
			}},
		}}

	blobData := serializeEvents(events)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		RawHistory: []*commonpb.DataBlob{
			blobData,
		},
		NextPageToken: nil,
	}

	var wid string
	getHistory := s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).Return(getResponse, nil).Times(1)
	getHistory.Do(func(ctx interface{}, getRequest *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) {
		wid = getRequest.Execution.WorkflowId
		s.NotNil(wid)
		s.NotEmpty(wid)
	})

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			TaskQueue:             taskqueue,
			WorkflowRunTimeout:    timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:   timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy: workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
	s.Equal(workflowRun.GetID(), wid)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Canceled() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED
	details := "some details"
	encodedDetails, _ := encodeArg(converter.GetDefaultDataConverter(), details)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionCanceledEventAttributes{WorkflowExecutionCanceledEventAttributes: &historypb.WorkflowExecutionCanceledEventAttributes{
						Details: encodedDetails,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute

	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Error(err)
	_, ok := err.(*WorkflowExecutionError)
	s.True(ok)
	var canceledErr *CanceledError
	s.True(errors.As(err, &canceledErr))
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Failed() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED
	reason := "some reason"
	details := "some details"
	err := NewApplicationError(reason, "", false, nil, details)
	var applicationErr *ApplicationError
	s.True(errors.As(err, &applicationErr))
	failure := GetDefaultFailureConverter().ErrorToFailure(applicationErr)

	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
						Failure: failure,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute

	err = workflowRun.Get(context.Background(), &decodedResult)
	_, ok := err.(*WorkflowExecutionError)
	s.True(ok)
	var applicationErr2 *ApplicationError
	s.True(errors.As(err, &applicationErr2))
	s.Equal(applicationErr.msg, applicationErr2.msg)
	s.Equal(applicationErr.nonRetryable, applicationErr2.nonRetryable)
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_Terminated() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType:  eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionTerminatedEventAttributes{WorkflowExecutionTerminatedEventAttributes: &historypb.WorkflowExecutionTerminatedEventAttributes{}}},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute

	err = workflowRun.Get(context.Background(), &decodedResult)
	_, ok := err.(*WorkflowExecutionError)
	s.True(ok)
	var terminatedErr *TerminatedError
	s.True(errors.As(err, &terminatedErr))
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_TimedOut() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionTimedOutEventAttributes{WorkflowExecutionTimedOutEventAttributes: &historypb.WorkflowExecutionTimedOutEventAttributes{
						RetryState: enumspb.RETRY_STATE_TIMEOUT,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any()).Return(getResponse, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute

	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Error(err)
	_, ok := err.(*WorkflowExecutionError)
	s.True(ok)
	var timeoutErr *TimeoutError
	s.True(errors.As(err, &timeoutErr))
	s.Equal(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, timeoutErr.TimeoutType())
	s.Equal(time.Minute, decodedResult)
}

func (s *workflowRunSuite) TestExecuteWorkflow_NoDup_ContinueAsNew() {
	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.workflowServiceClient.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).Times(1)

	newRunID := "some other random run ID"
	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType1 := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW
	getRequest1 := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse1 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType1,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionContinuedAsNewEventAttributes{WorkflowExecutionContinuedAsNewEventAttributes: &historypb.WorkflowExecutionContinuedAsNewEventAttributes{
						NewExecutionRunId: newRunID,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest1, gomock.Any()).Return(getResponse1, nil).Times(1)

	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(converter.GetDefaultDataConverter(), workflowResult)
	eventType2 := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	getRequest2 := getGetWorkflowExecutionHistoryRequest(filterType)
	getRequest2.Execution.RunId = newRunID
	getResponse2 := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType2,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest2, gomock.Any()).Return(getResponse2, nil).Times(1)

	workflowRun, err := s.workflowClient.ExecuteWorkflow(
		context.Background(),
		StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds * time.Second,
			WorkflowTaskTimeout:      timeoutInSeconds * time.Second,
			WorkflowIDReusePolicy:    workflowIDReusePolicy,
		}, workflowType,
	)
	s.Nil(err)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err = workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

func (s *workflowRunSuite) TestGetWorkflow() {
	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	eventType := enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED
	workflowResult := time.Hour * 59
	encodedResult, _ := encodeArg(converter.GetDefaultDataConverter(), workflowResult)
	getRequest := getGetWorkflowExecutionHistoryRequest(filterType)
	getResponse := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{
			Events: []*historypb.HistoryEvent{
				{
					EventType: eventType,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
						Result: encodedResult,
					}},
				},
			},
		},
		NextPageToken: nil,
	}
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getRequest, gomock.Any()).Return(getResponse, nil).Times(1)

	workflowID := workflowID
	runID := runID

	workflowRun := s.workflowClient.GetWorkflow(
		context.Background(),
		workflowID,
		runID,
	)
	s.Equal(workflowRun.GetID(), workflowID)
	s.Equal(workflowRun.GetRunID(), runID)
	decodedResult := time.Minute
	err := workflowRun.Get(context.Background(), &decodedResult)
	s.Nil(err)
	s.Equal(workflowResult, decodedResult)
}

// Verify that when `GetWorkflow` is called with no run ID, the current run ID is populated
func (s *workflowRunSuite) TestGetWorkflowNoRunId() {
	execution := &commonpb.WorkflowExecution{RunId: runID}
	describeResp := &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Execution: execution}}
	s.workflowServiceClient.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(describeResp, nil).Times(1)
	workflowRunNoRunID := s.workflowClient.GetWorkflow(
		context.Background(),
		workflowID,
		"",
	)
	s.Equal(runID, workflowRunNoRunID.GetRunID())
}

func (s *workflowRunSuite) TestGetWorkflowNoExtantWorkflowAndNoRunId() {
	describeResp := &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: nil}
	s.workflowServiceClient.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(describeResp, nil).Times(1)
	workflowRunNoRunID := s.workflowClient.GetWorkflow(
		context.Background(),
		workflowID,
		"",
	)
	s.Equal("", workflowRunNoRunID.GetRunID())
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_Retry() {
	s.workflowServiceClient.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.ExecuteMultiOperationResponse{
			Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{},
				},
				{
					// 1st response: empty response, Update is not durable yet, client retries
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{},
				},
			},
		}, nil).
		Return(&workflowservice.ExecuteMultiOperationResponse{
			Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{
						StartWorkflow: &workflowservice.StartWorkflowExecutionResponse{
							RunId: "RUN_ID",
						},
					},
				},
				{
					// 2nd response: non-empty response, Update is durable
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{
						UpdateWorkflow: &workflowservice.UpdateWorkflowExecutionResponse{
							Stage: enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
						},
					},
				},
			},
		}, nil)

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	_, err := s.workflowClient.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	s.NoError(err)
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_OperationNotExecuted() {

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := startOp.Get(ctxWithTimeout)
	require.EqualError(s.T(), err, "context deadline exceeded: operation was not executed")
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_Abort() {
	tests := []struct {
		name        string
		expectedErr string
		respFunc    func(ctx context.Context, in *workflowservice.ExecuteMultiOperationRequest, opts ...grpc.CallOption) (*workflowservice.ExecuteMultiOperationResponse, error)
	}{
		{
			name:        "Timeout",
			expectedErr: "context deadline exceeded",
			respFunc: func(ctx context.Context, in *workflowservice.ExecuteMultiOperationRequest, opts ...grpc.CallOption) (*workflowservice.ExecuteMultiOperationResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		{
			name:        "Cancelled",
			expectedErr: "was_cancelled",
			respFunc: func(ctx context.Context, in *workflowservice.ExecuteMultiOperationRequest, opts ...grpc.CallOption) (*workflowservice.ExecuteMultiOperationResponse, error) {
				return nil, serviceerror.NewCanceled("was_cancelled")
			},
		},
		{
			name:        "DeadlineExceeded",
			expectedErr: "deadline_exceeded",
			respFunc: func(ctx context.Context, in *workflowservice.ExecuteMultiOperationRequest, opts ...grpc.CallOption) (*workflowservice.ExecuteMultiOperationResponse, error) {
				return nil, serviceerror.NewDeadlineExceeded("deadline_exceeded")
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.workflowServiceClient.EXPECT().
				ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(tt.respFunc)

			startOp := s.workflowClient.NewWithStartWorkflowOperation(
				StartWorkflowOptions{
					ID:                       workflowID,
					WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
					TaskQueue:                taskqueue,
				}, workflowType,
			)

			ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			_, err := s.workflowClient.UpdateWithStartWorkflow(
				ctxWithTimeout,
				UpdateWithStartWorkflowOptions{
					UpdateOptions: UpdateWorkflowOptions{
						UpdateName:   "update",
						WaitForStage: WorkflowUpdateStageCompleted,
					},
					StartWorkflowOperation: startOp,
				},
			)

			var expectedErr *WorkflowUpdateServiceTimeoutOrCanceledError
			require.ErrorAs(s.T(), err, &expectedErr)
			require.ErrorContains(s.T(), err, tt.expectedErr)
		})
	}
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_NonMultiOperationError() {
	s.workflowServiceClient.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, serviceerror.NewInternal("internal error")).Times(1)

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	_, err := s.workflowClient.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	s.ErrorContains(err, "internal error")
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_ServerResponseCountMismatch() {
	s.workflowServiceClient.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.ExecuteMultiOperationResponse{
			Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{},
		}, nil).Times(1)

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	_, err := s.workflowClient.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	s.ErrorContains(err, "invalid server response: 0 instead of 2 operation results")
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_ServerErrorResponseCountMismatch() {
	s.workflowServiceClient.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, serviceerror.NewMultiOperationExecution("Error", []error{})).Times(1)

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	_, err := s.workflowClient.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	s.ErrorContains(err, "invalid server response: 0 instead of 2 operation errors")
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_ServerStartResponseTypeMismatch() {
	s.workflowServiceClient.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.ExecuteMultiOperationResponse{
			Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{}, // wrong!
				},
				nil,
			},
		}, nil).Times(1)

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	_, err := s.workflowClient.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	s.ErrorContains(err, "invalid server response: StartWorkflow response has the wrong type *workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow")
}

func (s *workflowRunSuite) TestExecuteWorkflowWithUpdate_ServerUpdateResponseTypeMismatch() {
	s.workflowServiceClient.EXPECT().
		ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.ExecuteMultiOperationResponse{
			Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{
						StartWorkflow: &workflowservice.StartWorkflowExecutionResponse{
							RunId: "RUN_ID",
						},
					},
				},
				{
					Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{}, // wrong!
				},
			},
		}, nil).Times(1)

	startOp := s.workflowClient.NewWithStartWorkflowOperation(
		StartWorkflowOptions{
			ID:                       workflowID,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			TaskQueue:                taskqueue,
		}, workflowType,
	)

	_, err := s.workflowClient.UpdateWithStartWorkflow(
		context.Background(),
		UpdateWithStartWorkflowOptions{
			UpdateOptions: UpdateWorkflowOptions{
				UpdateName:   "update",
				WaitForStage: WorkflowUpdateStageCompleted,
			},
			StartWorkflowOperation: startOp,
		},
	)
	s.ErrorContains(err, "invalid server response: UpdateWorkflow response has the wrong type *workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow")
}

func getGetWorkflowExecutionHistoryRequest(filterType enumspb.HistoryEventFilterType) *workflowservice.GetWorkflowExecutionHistoryRequest {
	request := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: DefaultNamespace,
		Execution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
		WaitNewEvent:           true,
		HistoryEventFilterType: filterType,
		SkipArchival:           true,
	}

	return request
}

// workflow client test suite
type (
	workflowClientTestSuite struct {
		suite.Suite
		mockCtrl      *gomock.Controller
		service       *workflowservicemock.MockWorkflowServiceClient
		client        Client
		dataConverter converter.DataConverter
	}
)

func TestWorkflowClientSuite(t *testing.T) {
	suite.Run(t, new(workflowClientTestSuite))
}

func (s *workflowClientTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	s.service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	s.client = NewServiceClient(s.service, nil, ClientOptions{})
	s.dataConverter = converter.GetDefaultDataConverter()
}

func (s *workflowClientTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *workflowClientTestSuite) TestSignalWithStartWorkflow() {
	signalName := "my signal"
	signalInput := []byte("my signal input")
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
	}

	startResponse := &workflowservice.SignalWithStartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.service.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(startResponse, nil).Times(2)

	resp, err := s.client.SignalWithStartWorkflow(context.Background(), workflowID, signalName, signalInput,
		options, workflowType)
	s.Nil(err)
	s.Equal(startResponse.GetRunId(), resp.GetRunID())

	options.ID = ""
	resp, err = s.client.SignalWithStartWorkflow(context.Background(), "", signalName, signalInput,
		options, workflowType)
	s.Nil(err)
	s.Equal(startResponse.GetRunId(), resp.GetRunID())
}

func (s *workflowClientTestSuite) TestSignalWithStartWorkflowWithContextAwareDataConverter() {
	dc := NewContextAwareDataConverter(converter.GetDefaultDataConverter())
	s.client = NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})
	client, ok := s.client.(*WorkflowClient)
	s.True(ok)

	input := "test"

	signalName := "my signal"
	signalInput := "my signal input"
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
	}

	startResponse := &workflowservice.SignalWithStartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.service.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(startResponse, nil).
		Do(func(_ interface{}, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...interface{}) {
			dc := client.dataConverter
			inputs := dc.ToStrings(req.Input)
			s.Equal("\"te?t\"", inputs[0])
			signalInputs := dc.ToStrings(req.SignalInput)
			s.Equal("\"my ?ignal input\"", signalInputs[0])
		})

	ctx := context.Background()
	ctx = context.WithValue(ctx, ContextAwareDataConverterContextKey, "s")

	resp, err := s.client.SignalWithStartWorkflow(ctx, workflowID, signalName, signalInput,
		options, workflowType, input)
	s.Nil(err)
	s.Equal(startResponse.GetRunId(), resp.GetRunID())
}

func (s *workflowClientTestSuite) TestSignalWithStartWorkflowValidation() {
	// ambiguous WorkflowID
	_, err := s.client.SignalWithStartWorkflow(
		context.Background(), "workflow-id-1", "my-signal", "my-signal-value",
		StartWorkflowOptions{ID: "workflow-id-2"}, workflowType)
	s.ErrorContains(err, "workflow ID from options not used")
}

func (s *workflowClientTestSuite) TestStartWorkflow() {
	client, ok := s.client.(*WorkflowClient)
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
	}
	f1 := func(ctx Context, r []byte) string {
		panic("this is just a stub")
	}

	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil)

	resp, err := client.ExecuteWorkflow(context.Background(), options, f1, []byte("test"))
	s.Equal(converter.GetDefaultDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.GetRunID())
}

func (s *workflowClientTestSuite) TestEagerStartWorkflowNotSupported() {
	client, ok := s.client.(*WorkflowClient)
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{
		EagerWorkflowStart: false,
	}

	var processTask bool
	eagerMock := &eagerWorkerMock{
		tryReserveSlotCallback: func() *SlotPermit { return &SlotPermit{} },
		processTaskAsyncCallback: func(task eagerTask) {
			processTask = true
		},
	}
	client.eagerDispatcher = &eagerWorkflowDispatcher{
		workersByTaskQueue: map[string]map[eagerWorker]struct{}{taskqueue: {eagerMock: {}}},
	}
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
		EnableEagerStart:         true,
	}
	f1 := func(ctx Context, r []byte) string {
		panic("this is just a stub")
	}

	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId:             runID,
		EagerWorkflowTask: &workflowservice.PollWorkflowTaskQueueResponse{},
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil)

	resp, err := client.ExecuteWorkflow(context.Background(), options, f1, []byte("test"))
	s.Equal(converter.GetDefaultDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.GetRunID())
	s.False(processTask)
	s.False(eagerMock.releaseCalled)
}

func (s *workflowClientTestSuite) TestEagerStartWorkflowNoWorker() {
	client, ok := s.client.(*WorkflowClient)
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{
		EagerWorkflowStart: false,
	}

	var processTask bool
	eagerMock := &eagerWorkerMock{
		tryReserveSlotCallback:   func() *SlotPermit { return nil },
		processTaskAsyncCallback: func(task eagerTask) { processTask = true }}
	client.eagerDispatcher = &eagerWorkflowDispatcher{
		workersByTaskQueue: map[string]map[eagerWorker]struct{}{taskqueue: {eagerMock: {}}},
	}
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
		EnableEagerStart:         true,
	}
	f1 := func(ctx Context, r []byte) string {
		panic("this is just a stub")
	}

	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId:             runID,
		EagerWorkflowTask: &workflowservice.PollWorkflowTaskQueueResponse{},
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil)

	resp, err := client.ExecuteWorkflow(context.Background(), options, f1, []byte("test"))
	s.Equal(converter.GetDefaultDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.GetRunID())
	s.False(processTask)
	s.False(eagerMock.releaseCalled)
}

func (s *workflowClientTestSuite) TestEagerStartWorkflow() {
	client, ok := s.client.(*WorkflowClient)
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{
		EagerWorkflowStart: true,
	}

	var processTask bool
	eagerMock := &eagerWorkerMock{
		tryReserveSlotCallback:   func() *SlotPermit { return &SlotPermit{} },
		processTaskAsyncCallback: func(task eagerTask) { processTask = true }}
	client.eagerDispatcher = &eagerWorkflowDispatcher{
		workersByTaskQueue: map[string]map[eagerWorker]struct{}{taskqueue: {eagerMock: {}}}}
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
		EnableEagerStart:         true,
	}
	f1 := func(ctx Context, r []byte) string {
		panic("this is just a stub")
	}

	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId:             runID,
		EagerWorkflowTask: &workflowservice.PollWorkflowTaskQueueResponse{},
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil)

	resp, err := client.ExecuteWorkflow(context.Background(), options, f1, []byte("test"))
	s.Equal(converter.GetDefaultDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.GetRunID())
	s.True(processTask)
	// Release will not have been called, since there is no real processor to call it
	// when the task is done.
	s.False(eagerMock.releaseCalled)
}

func (s *workflowClientTestSuite) TestEagerStartWorkflowStartRequestFail() {
	client, ok := s.client.(*WorkflowClient)
	client.capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{
		EagerWorkflowStart: true,
	}

	var processTask bool
	eagerMock := &eagerWorkerMock{
		tryReserveSlotCallback:   func() *SlotPermit { return &SlotPermit{} },
		processTaskAsyncCallback: func(task eagerTask) { processTask = true }}
	client.eagerDispatcher = &eagerWorkflowDispatcher{
		workersByTaskQueue: map[string]map[eagerWorker]struct{}{taskqueue: {eagerMock: {}}}}
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
		EnableEagerStart:         true,
	}
	f1 := func(ctx Context, r []byte) string {
		panic("this is just a stub")
	}

	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("failed request"))

	resp, err := client.ExecuteWorkflow(context.Background(), options, f1, []byte("test"))
	s.Nil(resp)
	s.Error(err)
	s.False(processTask)
	s.True(eagerMock.releaseCalled)
}

func (s *workflowClientTestSuite) TestExecuteWorkflowWithDataConverter() {
	dc := iconverter.NewTestDataConverter()
	s.client = NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})
	client, ok := s.client.(*WorkflowClient)
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
	}
	f1 := func(ctx Context, r []byte) string {
		panic("this is just a stub")
	}
	input := []byte("test")

	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).
		Do(func(_ interface{}, req *workflowservice.StartWorkflowExecutionRequest, _ ...interface{}) {
			dc := client.dataConverter
			encodedArg, _ := dc.ToPayloads(input)
			s.Equal(req.Input, encodedArg)
			var decodedArg []byte
			_ = dc.FromPayloads(req.Input, &decodedArg)
			s.Equal(input, decodedArg)
		})

	resp, err := client.ExecuteWorkflow(context.Background(), options, f1, input)
	s.Equal(iconverter.NewTestDataConverter(), client.dataConverter)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.GetRunID())
}

func (s *workflowClientTestSuite) TestExecuteWorkflowWithContextAwareDataConverter() {
	dc := NewContextAwareDataConverter(converter.GetDefaultDataConverter())
	s.client = NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})
	client, ok := s.client.(*WorkflowClient)
	s.True(ok)
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
	}
	f1 := func(ctx Context, s string) string {
		panic("this is just a stub")
	}
	input := "test"

	createResponse := &workflowservice.StartWorkflowExecutionResponse{
		RunId: runID,
	}
	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResponse, nil).
		Do(func(_ interface{}, req *workflowservice.StartWorkflowExecutionRequest, _ ...interface{}) {
			dc := client.dataConverter
			inputs := dc.ToStrings(req.Input)
			s.Equal("\"t?st\"", inputs[0])
		})

	ctx := context.Background()
	ctx = context.WithValue(ctx, ContextAwareDataConverterContextKey, "e")

	resp, err := client.ExecuteWorkflow(ctx, options, f1, input)
	s.Nil(err)
	s.Equal(createResponse.GetRunId(), resp.GetRunID())
}

func (s *workflowClientTestSuite) TestStartWorkflowWithMemoAndSearchAttr() {
	memo := map[string]interface{}{
		"testMemo": "memo value",
	}
	searchAttributes := map[string]interface{}{
		"testAttr": "attr value",
	}
	options := StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
		Memo:                     memo,
		SearchAttributes:         searchAttributes,
	}
	wf := func(ctx Context) string {
		panic("this is just a stub")
	}
	startResp := &workflowservice.StartWorkflowExecutionResponse{}

	s.service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(startResp, nil).
		Do(func(_ interface{}, req *workflowservice.StartWorkflowExecutionRequest, _ ...interface{}) {
			var resultMemo, resultAttr string
			err := converter.GetDefaultDataConverter().FromPayload(req.Memo.Fields["testMemo"], &resultMemo)
			s.NoError(err)
			s.Equal("memo value", resultMemo)

			err = converter.GetDefaultDataConverter().FromPayload(req.SearchAttributes.IndexedFields["testAttr"], &resultAttr)
			s.NoError(err)
			s.Equal("attr value", resultAttr)
		})
	_, _ = s.client.ExecuteWorkflow(context.Background(), options, wf)
}

func (s *workflowClientTestSuite) TestSignalWithStartWorkflowWithMemoAndSearchAttr() {
	memo := map[string]interface{}{
		"testMemo": "memo value",
	}
	searchAttributes := map[string]interface{}{
		"testAttr": "attr value",
	}
	options := StartWorkflowOptions{
		ID:                       "wid",
		TaskQueue:                taskqueue,
		WorkflowExecutionTimeout: timeoutInSeconds,
		WorkflowTaskTimeout:      timeoutInSeconds,
		Memo:                     memo,
		SearchAttributes:         searchAttributes,
	}
	wf := func(ctx Context) string {
		panic("this is just a stub")
	}
	startResp := &workflowservice.SignalWithStartWorkflowExecutionResponse{}

	s.service.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(startResp, nil).
		Do(func(_ interface{}, req *workflowservice.SignalWithStartWorkflowExecutionRequest, _ ...interface{}) {
			var resultMemo, resultAttr string
			err := converter.GetDefaultDataConverter().FromPayload(req.Memo.Fields["testMemo"], &resultMemo)
			s.NoError(err)
			s.Equal("memo value", resultMemo)

			err = converter.GetDefaultDataConverter().FromPayload(req.SearchAttributes.IndexedFields["testAttr"], &resultAttr)
			s.NoError(err)
			s.Equal("attr value", resultAttr)
		})
	_, _ = s.client.SignalWithStartWorkflow(context.Background(), "wid", "signal", "value", options, wf)
}

func (s *workflowClientTestSuite) TestGetWorkflowMemo() {
	var input1 map[string]interface{}
	result1, err := getWorkflowMemo(input1, s.dataConverter)
	s.NoError(err)
	s.Nil(result1)

	input1 = make(map[string]interface{})
	result2, err := getWorkflowMemo(input1, s.dataConverter)
	s.NoError(err)
	s.NotNil(result2)
	s.Equal(0, len(result2.Fields))

	input1["t1"] = "v1"
	result3, err := getWorkflowMemo(input1, s.dataConverter)
	s.NoError(err)
	s.NotNil(result3)
	s.Equal(1, len(result3.Fields))
	var resultString string
	// TODO (shtin): use s.DataConverter here???
	_ = converter.GetDefaultDataConverter().FromPayload(result3.Fields["t1"], &resultString)
	s.Equal("v1", resultString)

	input1["non-serializable"] = make(chan int)
	_, err = getWorkflowMemo(input1, s.dataConverter)
	s.Error(err)
}

func (s *workflowClientTestSuite) TestSerializeSearchAttributes() {
	var input1 map[string]interface{}
	result1, err := serializeUntypedSearchAttributes(input1)
	s.NoError(err)
	s.Nil(result1)

	input1 = make(map[string]interface{})
	result2, err := serializeUntypedSearchAttributes(input1)
	s.NoError(err)
	s.NotNil(result2)
	s.Equal(0, len(result2.IndexedFields))

	input1 = map[string]interface{}{
		"t1": "v1",
	}
	result3, err := serializeUntypedSearchAttributes(input1)
	s.NoError(err)
	s.NotNil(result3)
	s.Equal(1, len(result3.IndexedFields))
	var resultString string
	_ = converter.GetDefaultDataConverter().FromPayload(result3.IndexedFields["t1"], &resultString)
	s.Equal("v1", resultString)

	// *Payload type goes through.
	p, err := converter.GetDefaultDataConverter().ToPayload("5eaf00d")
	s.NoError(err)
	input1 = map[string]interface{}{
		"payload": p,
	}
	result4, err := serializeUntypedSearchAttributes(input1)
	s.NoError(err)
	s.NotNil(result3)
	s.Equal(1, len(result3.IndexedFields))
	_ = converter.GetDefaultDataConverter().FromPayload(result4.IndexedFields["payload"], &resultString)
	s.Equal("5eaf00d", resultString)

	input1 = map[string]interface{}{
		"non-serializable": make(chan int),
	}
	_, err = serializeUntypedSearchAttributes(input1)
	s.Error(err)
}

func (s *workflowClientTestSuite) TestListWorkflow() {
	request := &workflowservice.ListWorkflowExecutionsRequest{}
	response := &workflowservice.ListWorkflowExecutionsResponse{}
	s.service.EXPECT().ListWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil).
		Do(func(_ interface{}, req *workflowservice.ListWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal(DefaultNamespace, request.GetNamespace())
		})
	resp, err := s.client.ListWorkflow(context.Background(), request)
	s.Nil(err)
	s.Equal(response, resp)

	request.Namespace = "another"
	s.service.EXPECT().ListWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewInvalidArgument("")).
		Do(func(_ interface{}, req *workflowservice.ListWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal("another", request.GetNamespace())
		})
	_, err = s.client.ListWorkflow(context.Background(), request)
	s.IsType(&serviceerror.InvalidArgument{}, err)
}

func (s *workflowClientTestSuite) TestListArchivedWorkflow() {
	request := &workflowservice.ListArchivedWorkflowExecutionsRequest{}
	response := &workflowservice.ListArchivedWorkflowExecutionsResponse{}
	s.service.EXPECT().ListArchivedWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil).
		Do(func(_ interface{}, req *workflowservice.ListArchivedWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal(DefaultNamespace, request.GetNamespace())
		})
	ctxWithTimeout, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resp, err := s.client.ListArchivedWorkflow(ctxWithTimeout, request)
	s.Nil(err)
	s.Equal(response, resp)

	request.Namespace = "another"
	s.service.EXPECT().ListArchivedWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewInvalidArgument("")).
		Do(func(_ interface{}, req *workflowservice.ListArchivedWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal("another", request.GetNamespace())
		})
	_, err = s.client.ListArchivedWorkflow(ctxWithTimeout, request)
	s.IsType(&serviceerror.InvalidArgument{}, err)
}

func (s *workflowClientTestSuite) TestScanWorkflow() {
	request := &workflowservice.ScanWorkflowExecutionsRequest{}
	response := &workflowservice.ScanWorkflowExecutionsResponse{}
	s.service.EXPECT().ScanWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil).
		Do(func(_ interface{}, req *workflowservice.ScanWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal(DefaultNamespace, request.GetNamespace())
		})
	resp, err := s.client.ScanWorkflow(context.Background(), request)
	s.Nil(err)
	s.Equal(response, resp)

	request.Namespace = "another"
	s.service.EXPECT().ScanWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewInvalidArgument("")).
		Do(func(_ interface{}, req *workflowservice.ScanWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal("another", request.GetNamespace())
		})
	_, err = s.client.ScanWorkflow(context.Background(), request)
	s.IsType(&serviceerror.InvalidArgument{}, err)
}

func (s *workflowClientTestSuite) TestCountWorkflow() {
	request := &workflowservice.CountWorkflowExecutionsRequest{}
	response := &workflowservice.CountWorkflowExecutionsResponse{}
	s.service.EXPECT().CountWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil).
		Do(func(_ interface{}, req *workflowservice.CountWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal(DefaultNamespace, request.GetNamespace())
		})
	resp, err := s.client.CountWorkflow(context.Background(), request)
	s.Nil(err)
	s.Equal(response, resp)

	request.Namespace = "another"
	s.service.EXPECT().CountWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewInvalidArgument("")).
		Do(func(_ interface{}, req *workflowservice.CountWorkflowExecutionsRequest, _ ...interface{}) {
			s.Equal("another", request.GetNamespace())
		})
	_, err = s.client.CountWorkflow(context.Background(), request)
	s.IsType(&serviceerror.InvalidArgument{}, err)
}

func (s *workflowClientTestSuite) TestGetSearchAttributes() {
	response := &workflowservice.GetSearchAttributesResponse{}
	s.service.EXPECT().GetSearchAttributes(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil)
	resp, err := s.client.GetSearchAttributes(context.Background())
	s.Nil(err)
	s.Equal(response, resp)

	s.service.EXPECT().GetSearchAttributes(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, serviceerror.NewInvalidArgument(""))
	_, err = s.client.GetSearchAttributes(context.Background())
	s.IsType(&serviceerror.InvalidArgument{}, err)
}

func serializeEvents(events []*historypb.HistoryEvent) *commonpb.DataBlob {
	blob, _ := serializer.SerializeBatchEvents(events, enumspb.ENCODING_TYPE_PROTO3)

	return &commonpb.DataBlob{
		EncodingType: enumspb.ENCODING_TYPE_PROTO3,
		Data:         blob.Data,
	}
}

func TestClientCloseCount(t *testing.T) {
	// Create primary client
	server, err := startTestGRPCServer()
	require.NoError(t, err)
	defer server.Stop()
	client, err := DialClient(context.Background(), ClientOptions{HostPort: server.addr})
	require.NoError(t, err)
	workflowClient := client.(*WorkflowClient)

	// Confirm there is 1 unclosed client
	require.EqualValues(t, 1, atomic.LoadInt32(workflowClient.unclosedClients))

	// Create two more and confirm counts
	client2, err := NewClientFromExisting(context.Background(), client, ClientOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 2, atomic.LoadInt32(workflowClient.unclosedClients))
	require.Same(t, workflowClient.unclosedClients, client2.(*WorkflowClient).unclosedClients)
	client3, err := NewClientFromExisting(context.Background(), client, ClientOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 3, atomic.LoadInt32(workflowClient.unclosedClients))
	require.Same(t, workflowClient.unclosedClients, client3.(*WorkflowClient).unclosedClients)

	// Close the third one 3 times and confirm counts and that connection not
	// closed
	client3.Close()
	client3.Close()
	client3.Close()
	require.EqualValues(t, 2, atomic.LoadInt32(workflowClient.unclosedClients))
	require.NotSame(t, workflowClient.unclosedClients, client3.(*WorkflowClient).unclosedClients)
	require.Less(t, workflowClient.conn.GetState(), connectivity.Shutdown)

	// Close the primary one and confirm not closed
	client.Close()
	require.EqualValues(t, 1, atomic.LoadInt32(client2.(*WorkflowClient).unclosedClients))
	require.NotSame(t, workflowClient.unclosedClients, client2.(*WorkflowClient).unclosedClients)
	require.Less(t, workflowClient.conn.GetState(), connectivity.Shutdown)

	// Now close the last one (the second) and confirm it actually gets closed
	client2.Close()
	require.Equal(t, connectivity.Shutdown, workflowClient.conn.GetState())
}

func TestCompletedUpdateHandle(t *testing.T) {
	t.Run("error case", func(t *testing.T) {
		err := errors.New(t.Name())
		uh := completedUpdateHandle{err: err}
		require.Error(t, uh.Get(context.TODO(), nil))
	})

	t.Run("value case", func(t *testing.T) {
		dc := converter.GetDefaultDataConverter()
		payloads, err := dc.ToPayloads(t.Name())
		require.NoError(t, err)
		uh := completedUpdateHandle{value: newEncodedValue(payloads, dc)}
		var out string
		require.NoError(t, uh.Get(context.TODO(), &out))
		require.Equal(t, t.Name(), out)
	})

	t.Run("nil does not panic", func(t *testing.T) {
		uh := completedUpdateHandle{}
		require.NotPanics(t, func() { _ = uh.Get(context.TODO(), nil) })
	})
}

func TestUpdate(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	fc := GetDefaultFailureConverter()

	init := func(t *testing.T) (*workflowservicemock.MockWorkflowServiceClient, *WorkflowClient) {
		svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
		client := NewServiceClient(svc, nil, ClientOptions{})
		svc.EXPECT().
			GetSystemInfo(gomock.Any(), gomock.Any()).
			AnyTimes().
			Return(&workflowservice.GetSystemInfoResponse{}, nil)

		return svc, client
	}

	mustOutcome := func(t *testing.T, successOrError interface{}) *updatepb.Outcome {
		t.Helper()
		if errOut, ok := successOrError.(error); ok {
			return &updatepb.Outcome{
				Value: &updatepb.Outcome_Failure{
					Failure: fc.ErrorToFailure(errOut),
				},
			}
		}
		outSuccess, err := dc.ToPayloads(successOrError)
		require.NoError(t, err)
		return &updatepb.Outcome{
			Value: &updatepb.Outcome_Success{
				Success: outSuccess,
			},
		}

	}

	const (
		sync  = WorkflowUpdateStageCompleted
		async = WorkflowUpdateStageAccepted
	)

	newRequest := func(
		t *testing.T,
		stage WorkflowUpdateStage,
	) UpdateWorkflowOptions {
		t.Helper()
		return UpdateWorkflowOptions{
			UpdateID:     fmt.Sprintf("%v-update_id", t.Name()),
			WorkflowID:   fmt.Sprintf("%v-workflow_id", t.Name()),
			RunID:        fmt.Sprintf("%v-run_id", t.Name()),
			UpdateName:   fmt.Sprintf("%v-update_name", t.Name()),
			WaitForStage: stage,
		}
	}

	refFromRequest := func(opt UpdateWorkflowOptions) *updatepb.UpdateRef {
		return &updatepb.UpdateRef{
			WorkflowExecution: &commonpb.WorkflowExecution{
				WorkflowId: opt.WorkflowID,
				RunId:      opt.RunID,
			},
			UpdateId: opt.UpdateName,
		}
	}

	sleepCtx := func(ctx context.Context, dur time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(dur):
		}
	}

	t.Run("sync success", func(t *testing.T) {
		svc, client := init(t)
		want := t.Name()
		req := newRequest(t, sync)
		svc.EXPECT().
			UpdateWorkflowExecution(gomock.Any(), gomock.Any()).Return(
			&workflowservice.UpdateWorkflowExecutionResponse{
				UpdateRef: refFromRequest(req),
				Outcome:   mustOutcome(t, want),
				Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
			},
			nil,
		)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.NoError(t, err)
		require.Equal(t, want, got)
		// Verify that calling Get with nil does not panic
		err = handle.Get(context.TODO(), nil)
		require.NoError(t, err)
	})
	t.Run("sync error", func(t *testing.T) {
		svc, client := init(t)
		want := errors.New("this error was intentional")
		req := newRequest(t, sync)
		svc.EXPECT().
			UpdateWorkflowExecution(gomock.Any(), gomock.Any()).Return(
			&workflowservice.UpdateWorkflowExecutionResponse{
				UpdateRef: refFromRequest(req),
				Outcome:   mustOutcome(t, want),
				Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
			},
			nil,
		)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.Error(t, err)
		require.ErrorContains(t, err, want.Error())
	})
	t.Run("async success", func(t *testing.T) {
		svc, client := init(t)
		want := t.Name()
		req := newRequest(t, async)
		svc.EXPECT().UpdateWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil, // async invocation - outcome unknown
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ACCEPTED,
				},
				nil,
			)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.PollWorkflowExecutionUpdateResponse{
					Outcome: mustOutcome(t, want),
				},
				nil,
			).Times(2)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.NoError(t, err)
		require.Equal(t, want, got)
		// Verify that calling Get with nil does not panic
		err = handle.Get(context.TODO(), nil)
		require.NoError(t, err)
	})
	t.Run("async delayed accepted", func(t *testing.T) {
		svc, client := init(t)
		want := errors.New("this error was intentional")
		req := newRequest(t, async)
		svc.EXPECT().UpdateWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil, // async invocation - outcome unknown
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ACCEPTED,
				},
				nil,
			)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.PollWorkflowExecutionUpdateResponse{
					Outcome: mustOutcome(t, want),
				},
				nil,
			)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.Error(t, err)
		require.ErrorContains(t, err, want.Error())
	})
	t.Run("admitted error", func(t *testing.T) {
		svc, client := init(t)
		want := t.Name()
		req := newRequest(t, async)
		svc.EXPECT().UpdateWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil, // async invocation - outcome unknown
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ADMITTED,
				},
				nil,
			).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil, // async invocation - outcome unknown
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ACCEPTED,
				},
				nil,
			)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.PollWorkflowExecutionUpdateResponse{
					Outcome: mustOutcome(t, want),
				},
				nil,
			).Times(2)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.NoError(t, err)
		require.Equal(t, want, got)
		// Verify that calling Get with nil does not panic
		err = handle.Get(context.TODO(), nil)
		require.NoError(t, err)
	})
	t.Run("internal retry on nil outcome", func(t *testing.T) {
		svc, client := init(t)
		want := t.Name()
		req := newRequest(t, async)
		svc.EXPECT().UpdateWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil, // async invocation - outcome unknown
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ACCEPTED,
				},
				nil,
			)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.PollWorkflowExecutionUpdateResponse{
					Outcome: nil, // not an error, outcome still unknown
				},
				nil,
			)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.PollWorkflowExecutionUpdateResponse{
					Outcome: mustOutcome(t, want),
				},
				nil,
			)

		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
	t.Run("default ctx timeout", func(t *testing.T) {
		svc, client := init(t)
		handle := client.GetWorkflowUpdateHandle(GetWorkflowUpdateHandleOptions{})
		expectedDeadline := time.Now().Add(pollUpdateTimeout)
		var actualDeadline time.Time // assigned below in mock
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(
					ctx context.Context,
					_ *workflowservice.PollWorkflowExecutionUpdateRequest,
					_ ...grpc.CallOption,
				) (*workflowservice.PollWorkflowExecutionUpdateResponse, error) {
					actualDeadline, _ = ctx.Deadline()
					return nil, errors.New("intentional error")
				},
			)
		_ = handle.Get(context.TODO(), nil)

		// can't tell what the exact deadline will be so assert that the
		// observed deadline passed to server rpc is within 2 seconds of the
		// default pollUpdateTimout that is used when no other deadline/timeout
		// is supplied by the caller.
		require.WithinDuration(t, expectedDeadline, actualDeadline, 2*time.Second)
	})
	t.Run("parent ctx timeout", func(t *testing.T) {
		svc, client := init(t)
		handle := client.GetWorkflowUpdateHandle(GetWorkflowUpdateHandleOptions{})
		callerDeadline := time.Now().Add(50 * time.Millisecond)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(
					ctx context.Context,
					_ *workflowservice.PollWorkflowExecutionUpdateRequest,
					_ ...grpc.CallOption,
				) (*workflowservice.PollWorkflowExecutionUpdateResponse, error) {
					thisDeadline, ok := ctx.Deadline()
					require.True(t, ok)
					require.LessOrEqual(t, thisDeadline, callerDeadline,
						"caller timeout can be shortened but not extended")
					ctxWillTimeoutIn := time.Until(thisDeadline)
					sleepCtx(ctx, ctxWillTimeoutIn+3*time.Second)
					require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
					return nil, status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
				},
			)
		callerCtx, cancel := context.WithDeadline(context.TODO(), callerDeadline)
		defer cancel()
		var got string
		err := handle.Get(callerCtx, &got)
		require.Error(t, err)
		var rpcErr *WorkflowUpdateServiceTimeoutOrCanceledError
		require.ErrorAs(t, err, &rpcErr)
	})
	t.Run("parent ctx cancelled", func(t *testing.T) {
		svc, client := init(t)
		handle := client.GetWorkflowUpdateHandle(GetWorkflowUpdateHandleOptions{})
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(
					ctx context.Context,
					_ *workflowservice.PollWorkflowExecutionUpdateRequest,
					_ ...grpc.CallOption,
				) (*workflowservice.PollWorkflowExecutionUpdateResponse, error) {
					return nil, status.Error(codes.Canceled, context.Canceled.Error())
				},
			)
		callerCtx, cancel := context.WithCancel(context.TODO())
		cancel()
		var got string
		err := handle.Get(callerCtx, &got)
		require.Error(t, err)
		var rpcErr *WorkflowUpdateServiceTimeoutOrCanceledError
		require.ErrorAs(t, err, &rpcErr)
		require.Contains(t, err.Error(), "context canceled")
	})
	t.Run("sync delayed success", func(t *testing.T) {
		svc, client := init(t)
		want := t.Name()
		req := newRequest(t, sync)
		svc.EXPECT().
			UpdateWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil,
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ADMITTED,
				},
				nil,
			).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   mustOutcome(t, want),
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
				},
				nil,
			)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.NoError(t, err)
		require.Equal(t, want, got)
		// Verify that calling Get with nil does not panic
		err = handle.Get(context.TODO(), nil)
		require.NoError(t, err)
	})
	t.Run("sync multiple step success", func(t *testing.T) {
		svc, client := init(t)
		want := t.Name()
		req := newRequest(t, sync)
		svc.EXPECT().
			UpdateWorkflowExecution(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil,
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ADMITTED,
				},
				nil,
			).
			Return(
				&workflowservice.UpdateWorkflowExecutionResponse{
					UpdateRef: refFromRequest(req),
					Outcome:   nil,
					Stage:     enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_ACCEPTED,
				},
				nil,
			)
		svc.EXPECT().PollWorkflowExecutionUpdate(gomock.Any(), gomock.Any()).
			Return(
				&workflowservice.PollWorkflowExecutionUpdateResponse{
					Outcome: mustOutcome(t, want),
					Stage:   enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
				},
				nil,
			).Times(1)
		handle, err := client.UpdateWorkflow(context.TODO(), req)
		require.NoError(t, err)
		var got string
		err = handle.Get(context.TODO(), &got)
		require.NoError(t, err)
		require.Equal(t, want, got)
		// Verify that calling Get with nil does not panic
		err = handle.Get(context.TODO(), nil)
		require.NoError(t, err)
	})
}
