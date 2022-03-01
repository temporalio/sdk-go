// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

// Code generated by go generate; DO NOT EDIT.

package converter

import (
	"context"

	"google.golang.org/grpc"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	querypb "go.temporal.io/api/query/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
)

// PayloadEncoderGRPCClientInterceptorOptions holds interceptor options.
// Currently this is just the list of encoders to use.
type PayloadEncoderGRPCClientInterceptorOptions struct {
	Encoders []PayloadEncoder
}

// NewPayloadEncoderGRPCClientInterceptor returns a GRPC Client Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a matching EncodingDataConverter.
// Note: This approach does not support use cases that rely on the ContextAware DataConverter interface as
// workflow context is not available at the GRPC level.
func NewPayloadEncoderGRPCClientInterceptor(options PayloadEncoderGRPCClientInterceptorOptions) (grpc.UnaryClientInterceptor, error) {
	s := serviceInterceptor{encoders: options.Encoders}

	return func(ctx context.Context, method string, req, response interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := s.process(true, req)
		if err != nil {
			return err
		}

		err = invoker(ctx, method, req, response, cc, opts...)
		if err != nil {
			return err
		}

		return s.process(false, response)
	}, nil
}

type serviceInterceptor struct {
	encoders []PayloadEncoder
}

func (s *serviceInterceptor) encodePayload(payload *commonpb.Payload) error {
	for i := len(s.encoders) - 1; i >= 0; i-- {
		if err := s.encoders[i].Encode(payload); err != nil {
			return err
		}
	}
	return nil
}

func (s *serviceInterceptor) decodePayload(payload *commonpb.Payload) error {
	for _, encoder := range s.encoders {
		if err := encoder.Decode(payload); err != nil {
			return err
		}
	}
	return nil
}

func (s *serviceInterceptor) process(encode bool, objs ...interface{}) error {
	for _, obj := range objs {
		switch o := obj.(type) {
		case *commonpb.Payload:
			if encode {
				if err := s.encodePayload(o); err != nil {
					return err
				}
			} else {
				if err := s.decodePayload(o); err != nil {
					return err
				}
			}
		case *commonpb.Payloads:
			for _, x := range o.GetPayloads() {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}
		case map[string]*commonpb.Payload:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *commandpb.CancelWorkflowExecutionCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case []*commandpb.Command:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *commandpb.Command:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetCancelWorkflowExecutionCommandAttributes(),
				o.GetCompleteWorkflowExecutionCommandAttributes(),
				o.GetContinueAsNewWorkflowExecutionCommandAttributes(),
				o.GetFailWorkflowExecutionCommandAttributes(),
				o.GetRecordMarkerCommandAttributes(),
				o.GetScheduleActivityTaskCommandAttributes(),
				o.GetSignalExternalWorkflowExecutionCommandAttributes(),
				o.GetStartChildWorkflowExecutionCommandAttributes(),
			); err != nil {
				return err
			}

		case *commandpb.CompleteWorkflowExecutionCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetResult(),
			); err != nil {
				return err
			}

		case *commandpb.ContinueAsNewWorkflowExecutionCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
				o.GetHeader(),
				o.GetInput(),
				o.GetLastCompletionResult(),
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *commandpb.FailWorkflowExecutionCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *commandpb.RecordMarkerCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
				o.GetFailure(),
				o.GetHeader(),
			); err != nil {
				return err
			}

		case *commandpb.ScheduleActivityTaskCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *commandpb.SignalExternalWorkflowExecutionCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *commandpb.StartChildWorkflowExecutionCommandAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *commonpb.Header:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFields(),
			); err != nil {
				return err
			}

		case *commonpb.Memo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFields(),
			); err != nil {
				return err
			}

		case *failurepb.ApplicationFailureInfo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *failurepb.CanceledFailureInfo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *failurepb.Failure:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetApplicationFailureInfo(),
				o.GetCanceledFailureInfo(),
				o.GetCause(),
				o.GetResetWorkflowFailureInfo(),
				o.GetTimeoutFailureInfo(),
			); err != nil {
				return err
			}

		case *failurepb.ResetWorkflowFailureInfo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetLastHeartbeatDetails(),
			); err != nil {
				return err
			}

		case *failurepb.TimeoutFailureInfo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetLastHeartbeatDetails(),
			); err != nil {
				return err
			}

		case *historypb.ActivityTaskCanceledEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *historypb.ActivityTaskCompletedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetResult(),
			); err != nil {
				return err
			}

		case *historypb.ActivityTaskFailedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *historypb.ActivityTaskScheduledEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *historypb.ActivityTaskStartedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetLastFailure(),
			); err != nil {
				return err
			}

		case *historypb.ActivityTaskTimedOutEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *historypb.ChildWorkflowExecutionCanceledEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *historypb.ChildWorkflowExecutionCompletedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetResult(),
			); err != nil {
				return err
			}

		case *historypb.ChildWorkflowExecutionFailedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *historypb.ChildWorkflowExecutionStartedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
			); err != nil {
				return err
			}

		case *historypb.History:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetEvents(),
			); err != nil {
				return err
			}

		case []*historypb.HistoryEvent:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *historypb.HistoryEvent:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetActivityTaskCanceledEventAttributes(),
				o.GetActivityTaskCompletedEventAttributes(),
				o.GetActivityTaskFailedEventAttributes(),
				o.GetActivityTaskScheduledEventAttributes(),
				o.GetActivityTaskStartedEventAttributes(),
				o.GetActivityTaskTimedOutEventAttributes(),
				o.GetChildWorkflowExecutionCanceledEventAttributes(),
				o.GetChildWorkflowExecutionCompletedEventAttributes(),
				o.GetChildWorkflowExecutionFailedEventAttributes(),
				o.GetChildWorkflowExecutionStartedEventAttributes(),
				o.GetMarkerRecordedEventAttributes(),
				o.GetSignalExternalWorkflowExecutionInitiatedEventAttributes(),
				o.GetStartChildWorkflowExecutionInitiatedEventAttributes(),
				o.GetWorkflowExecutionCanceledEventAttributes(),
				o.GetWorkflowExecutionCompletedEventAttributes(),
				o.GetWorkflowExecutionContinuedAsNewEventAttributes(),
				o.GetWorkflowExecutionFailedEventAttributes(),
				o.GetWorkflowExecutionSignaledEventAttributes(),
				o.GetWorkflowExecutionStartedEventAttributes(),
				o.GetWorkflowExecutionTerminatedEventAttributes(),
				o.GetWorkflowTaskFailedEventAttributes(),
			); err != nil {
				return err
			}

		case *historypb.MarkerRecordedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
				o.GetFailure(),
				o.GetHeader(),
			); err != nil {
				return err
			}

		case *historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *historypb.StartChildWorkflowExecutionInitiatedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionCanceledEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionCompletedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetResult(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionContinuedAsNewEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
				o.GetHeader(),
				o.GetInput(),
				o.GetLastCompletionResult(),
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionFailedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionSignaledEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionStartedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetContinuedFailure(),
				o.GetHeader(),
				o.GetInput(),
				o.GetLastCompletionResult(),
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowExecutionTerminatedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *historypb.WorkflowTaskFailedEventAttributes:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case map[string]*querypb.WorkflowQuery:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *querypb.WorkflowQuery:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetQueryArgs(),
			); err != nil {
				return err
			}

		case map[string]*querypb.WorkflowQueryResult:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *querypb.WorkflowQueryResult:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetAnswer(),
			); err != nil {
				return err
			}

		case []*workflowpb.PendingActivityInfo:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *workflowpb.PendingActivityInfo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeartbeatDetails(),
				o.GetLastFailure(),
			); err != nil {
				return err
			}

		case []*workflowpb.WorkflowExecutionInfo:
			for _, x := range o {
				if err := s.process(encode, x); err != nil {
					return err
				}
			}

		case *workflowpb.WorkflowExecutionInfo:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *workflowservicepb.DescribeWorkflowExecutionResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetPendingActivities(),
				o.GetWorkflowExecutionInfo(),
			); err != nil {
				return err
			}

		case *workflowservicepb.GetWorkflowExecutionHistoryResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHistory(),
			); err != nil {
				return err
			}

		case *workflowservicepb.ListArchivedWorkflowExecutionsResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetExecutions(),
			); err != nil {
				return err
			}

		case *workflowservicepb.ListClosedWorkflowExecutionsResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetExecutions(),
			); err != nil {
				return err
			}

		case *workflowservicepb.ListOpenWorkflowExecutionsResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetExecutions(),
			); err != nil {
				return err
			}

		case *workflowservicepb.ListWorkflowExecutionsResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetExecutions(),
			); err != nil {
				return err
			}

		case *workflowservicepb.PollActivityTaskQueueResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetHeartbeatDetails(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *workflowservicepb.PollWorkflowTaskQueueResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHistory(),
				o.GetQueries(),
				o.GetQuery(),
			); err != nil {
				return err
			}

		case *workflowservicepb.QueryWorkflowRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetQuery(),
			); err != nil {
				return err
			}

		case *workflowservicepb.QueryWorkflowResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetQueryResult(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RecordActivityTaskHeartbeatByIdRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RecordActivityTaskHeartbeatRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondActivityTaskCanceledByIdRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondActivityTaskCanceledRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondActivityTaskCompletedByIdRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetResult(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondActivityTaskCompletedRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetResult(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondActivityTaskFailedByIdRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondActivityTaskFailedRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondQueryTaskCompletedRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetQueryResult(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondWorkflowTaskCompletedRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetCommands(),
				o.GetQueryResults(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondWorkflowTaskCompletedResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetWorkflowTask(),
			); err != nil {
				return err
			}

		case *workflowservicepb.RespondWorkflowTaskFailedRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetFailure(),
			); err != nil {
				return err
			}

		case *workflowservicepb.ScanWorkflowExecutionsResponse:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetExecutions(),
			); err != nil {
				return err
			}

		case *workflowservicepb.SignalWithStartWorkflowExecutionRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
				o.GetMemo(),
				o.GetSignalInput(),
			); err != nil {
				return err
			}

		case *workflowservicepb.SignalWorkflowExecutionRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
			); err != nil {
				return err
			}

		case *workflowservicepb.StartWorkflowExecutionRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetHeader(),
				o.GetInput(),
				o.GetMemo(),
			); err != nil {
				return err
			}

		case *workflowservicepb.TerminateWorkflowExecutionRequest:
			if o == nil {
				continue
			}
			if err := s.process(
				encode,
				o.GetDetails(),
			); err != nil {
				return err
			}

		}
	}

	return nil
}
