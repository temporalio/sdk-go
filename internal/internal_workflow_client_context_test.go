package internal

import (
	"context"
	"errors"

	"github.com/golang/mock/gomock"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
)

type decodeContextKey struct{}

// decodeContextDataConverter prefixes every decoded string with a value taken
// from the context passed to WithContext.
type decodeContextDataConverter struct {
	converter.DataConverter
	prefix string
}

func (dc *decodeContextDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if err := dc.DataConverter.FromPayload(payload, valuePtr); err != nil {
		return err
	}
	if str, ok := valuePtr.(*string); ok {
		*str = dc.prefix + *str
	}
	return nil
}

func (dc *decodeContextDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	for i, payload := range payloads.GetPayloads() {
		if i >= len(valuePtrs) {
			break
		}
		if err := dc.FromPayload(payload, valuePtrs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (dc *decodeContextDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	prefix, ok := ctx.Value(decodeContextKey{}).(string)
	if !ok {
		return dc
	}
	return &decodeContextDataConverter{DataConverter: dc.DataConverter, prefix: prefix}
}

func (s *workflowRunSuite) TestGetUsesContextAwareDataConverter() {
	dc := &decodeContextDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	s.workflowClient = NewServiceClient(s.workflowServiceClient, nil, ClientOptions{DataConverter: dc})

	encodedResult, err := encodeArg(converter.GetDefaultDataConverter(), "result")
	s.NoError(err)
	encodedDetails, err := encodeArg(converter.GetDefaultDataConverter(), "details")
	s.NoError(err)

	completed := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{Events: []*historypb.HistoryEvent{{
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
				WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: encodedResult},
			},
		}}},
	}
	canceled := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{Events: []*historypb.HistoryEvent{{
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCanceledEventAttributes{
				WorkflowExecutionCanceledEventAttributes: &historypb.WorkflowExecutionCanceledEventAttributes{Details: encodedDetails},
			},
		}}},
	}
	filterType := enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getGetWorkflowExecutionHistoryRequest(filterType), gomock.Any()).
		Return(completed, nil).Times(1)
	s.workflowServiceClient.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), getGetWorkflowExecutionHistoryRequest(filterType), gomock.Any()).
		Return(canceled, nil).Times(1)

	ctx := context.WithValue(context.Background(), decodeContextKey{}, "ctx:")
	run := s.workflowClient.GetWorkflow(ctx, workflowID, runID)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal("ctx:result", result)

	err = run.Get(ctx, &result)
	var canceledErr *CanceledError
	s.True(errors.As(err, &canceledErr))
	var details string
	s.NoError(canceledErr.Details(&details))
	s.Equal("ctx:details", details)
}

func (s *workflowClientTestSuite) TestQueryWorkflowUsesContextAwareDataConverter() {
	dc := &decodeContextDataConverter{DataConverter: converter.GetDefaultDataConverter()}
	s.client = NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})

	encodedResult, err := encodeArg(converter.GetDefaultDataConverter(), "result")
	s.NoError(err)
	s.service.EXPECT().QueryWorkflow(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.QueryWorkflowResponse{QueryResult: encodedResult}, nil)

	ctx := context.WithValue(context.Background(), decodeContextKey{}, "ctx:")
	value, err := s.client.QueryWorkflow(ctx, workflowID, runID, "my-query")
	s.NoError(err)
	var result string
	s.NoError(value.Get(&result))
	s.Equal("ctx:result", result)
}

func (dc *decodeContextDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	return dc
}
