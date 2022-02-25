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

package converter

import (
	"context"

	"go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

type serviceInterceptor struct {
	encoders []PayloadEncoder
}

func (s *serviceInterceptor) processRequest(req interface{}) error {
	switch r := req.(type) {
	case *workflowservice.StartWorkflowExecutionRequest:
		return encodePayloads(r.Input, s.encoders...)
	case *workflowservice.SignalWorkflowExecutionRequest:
		return encodePayloads(r.Input, s.encoders...)
	case *workflowservice.SignalWithStartWorkflowExecutionRequest:
		err := encodePayloads(r.Input, s.encoders...)
		if err != nil {
			return err
		}

		return encodePayloads(r.SignalInput, s.encoders...)
	case *workflowservice.RespondActivityTaskCompletedRequest:
		return encodePayloads(r.Result, s.encoders...)
	case *workflowservice.RespondActivityTaskCompletedByIdRequest:
		return encodePayloads(r.Result, s.encoders...)
	case *workflowservice.RespondWorkflowTaskCompletedRequest:
		return s.processCommands(r.Commands)
	case *workflowservice.RecordActivityTaskHeartbeatRequest:
		return encodePayloads(r.Details, s.encoders...)
	case *workflowservice.RecordActivityTaskHeartbeatByIdRequest:
		return encodePayloads(r.Details, s.encoders...)
	}

	return nil
}

func (s *serviceInterceptor) processResponse(response interface{}) error {
	switch r := response.(type) {
	case *workflowservice.GetWorkflowExecutionHistoryResponse:
		return s.processEvents(r.History.Events)
	case *workflowservice.PollWorkflowTaskQueueResponse:
		if r.WorkflowType != nil {
			return s.processEvents(r.History.Events)
		}
	case *workflowservice.PollActivityTaskQueueResponse:
		if r.Input != nil {
			err := decodePayloads(r.Input, s.encoders...)
			if err != nil {
				return err
			}
		}

		if r.HeartbeatDetails != nil {
			return decodePayloads(r.HeartbeatDetails, s.encoders...)
		}
	}

	return nil
}

func (s *serviceInterceptor) processCommands(commands []*command.Command) error {
	var err error
	for _, c := range commands {
		switch c.CommandType {
		case enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK:
			err = encodePayloads(c.GetScheduleActivityTaskCommandAttributes().Input, s.encoders...)
		case enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION:
			err = encodePayloads(c.GetCompleteWorkflowExecutionCommandAttributes().Result, s.encoders...)
		case enumspb.COMMAND_TYPE_CONTINUE_AS_NEW_WORKFLOW_EXECUTION:
			err = encodePayloads(c.GetContinueAsNewWorkflowExecutionCommandAttributes().Input, s.encoders...)
		case enumspb.COMMAND_TYPE_START_CHILD_WORKFLOW_EXECUTION:
			err = encodePayloads(c.GetStartChildWorkflowExecutionCommandAttributes().Input, s.encoders...)
		case enumspb.COMMAND_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION:
			err = encodePayloads(c.GetSignalExternalWorkflowExecutionCommandAttributes().Input, s.encoders...)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *serviceInterceptor) processEvents(events []*historypb.HistoryEvent) error {
	var err error

	for _, e := range events {
		switch e.EventType {
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			err = decodePayloads(e.GetWorkflowExecutionStartedEventAttributes().Input, s.encoders...)
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
			err = decodePayloads(e.GetWorkflowExecutionCompletedEventAttributes().Result, s.encoders...)
		case enumspb.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED:
			err = decodePayloads(e.GetStartChildWorkflowExecutionInitiatedEventAttributes().Input, s.encoders...)
		case enumspb.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
			err = decodePayloads(e.GetSignalExternalWorkflowExecutionInitiatedEventAttributes().Input, s.encoders...)
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
			err = decodePayloads(e.GetWorkflowExecutionContinuedAsNewEventAttributes().Input, s.encoders...)
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			err = decodePayloads(e.GetActivityTaskScheduledEventAttributes().Input, s.encoders...)
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			err = decodePayloads(e.GetActivityTaskCompletedEventAttributes().Result, s.encoders...)
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
			err = decodePayloads(e.GetWorkflowExecutionSignaledEventAttributes().Input, s.encoders...)
		case enumspb.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
			err = decodePayloads(e.GetChildWorkflowExecutionCompletedEventAttributes().Result, s.encoders...)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// NewPayloadEncoderGRPCServerInterceptor returns a GRPC Server Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a matching EncodingDataConverter.
// Note: This approach does not support use cases that rely on the ContextAware DataConverter interface as
// workflow context is not available at the GRPC level.
func NewPayloadEncoderGRPCServerInterceptor(encoders ...PayloadEncoder) grpc.UnaryServerInterceptor {
	s := serviceInterceptor{encoders: encoders}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		err := s.processRequest(req)
		if err != nil {
			return nil, err
		}

		resp, err := handler(ctx, req)
		if err != nil {
			return nil, err
		}

		return s.processResponse(resp), err
	}
}

// NewPayloadEncoderGRPCClientInterceptor returns a GRPC Client Interceptor that will mimic the encoding
// that the SDK system would perform when configured with a matching EncodingDataConverter.
// Note: This approach does not support use cases that rely on the ContextAware DataConverter interface as
// workflow context is not available at the GRPC level.
func NewPayloadEncoderGRPCClientInterceptor(encoders ...PayloadEncoder) grpc.UnaryClientInterceptor {
	s := serviceInterceptor{encoders: encoders}

	return func(ctx context.Context, method string, req, response interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := s.processRequest(req)
		if err != nil {
			return err
		}

		err = invoker(ctx, method, req, response, cc, opts...)
		if err != nil {
			return err
		}

		return s.processResponse(response)
	}
}

func encodePayloads(payloads *commonpb.Payloads, encoders ...PayloadEncoder) error {
	for _, payload := range payloads.Payloads {
		for i := len(encoders) - 1; i >= 0; i-- {
			if err := encoders[i].Encode(payload); err != nil {
				return err
			}
		}
	}

	return nil
}

func decodePayloads(payloads *commonpb.Payloads, encoders ...PayloadEncoder) error {
	for _, payload := range payloads.Payloads {
		for _, encoder := range encoders {
			if err := encoder.Decode(payload); err != nil {
				return err
			}
		}
	}

	return nil
}
