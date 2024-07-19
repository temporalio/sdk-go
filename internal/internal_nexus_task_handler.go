// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

func nexusHandlerError(t nexus.HandlerErrorType, message string) *nexuspb.HandlerError {
	return &nexuspb.HandlerError{
		ErrorType: string(t),
		Failure: &nexuspb.Failure{
			Message: message,
		},
	}
}

func nexusHandlerErrorToProto(handlerErr *nexus.HandlerError) *nexuspb.HandlerError {
	pbHandlerErr := &nexuspb.HandlerError{
		ErrorType: string(handlerErr.Type),
	}
	if handlerErr.Failure != nil {
		pbHandlerErr.Failure = &nexuspb.Failure{
			Message:  handlerErr.Failure.Message,
			Metadata: handlerErr.Failure.Metadata,
			Details:  handlerErr.Failure.Details,
		}
	}
	return pbHandlerErr
}

type nexusTaskHandler struct {
	nexusHandler   nexus.Handler
	identity       string
	namespace      string
	taskQueueName  string
	client         Client
	dataConverter  converter.DataConverter
	logger         log.Logger
	metricsHandler metrics.Handler
}

func newNexusTaskHandler(
	nexusHandler nexus.Handler,
	identity string,
	namespace string,
	taskQueueName string,
	client Client,
	dataConverter converter.DataConverter,
	logger log.Logger,
	metricsHandler metrics.Handler,
) *nexusTaskHandler {
	return &nexusTaskHandler{
		nexusHandler:   nexusHandler,
		logger:         logger,
		dataConverter:  dataConverter,
		identity:       identity,
		namespace:      namespace,
		taskQueueName:  taskQueueName,
		client:         client,
		metricsHandler: metricsHandler,
	}
}

func (h *nexusTaskHandler) Execute(task *workflowservice.PollNexusTaskQueueResponse) (*workflowservice.RespondNexusTaskCompletedRequest, *workflowservice.RespondNexusTaskFailedRequest, error) {
	res, handlerErr, err := h.execute(task)
	if err != nil {
		return nil, nil, err
	}
	if handlerErr != nil {
		return nil, h.fillInFailure(task.TaskToken, handlerErr), nil
	}
	return h.fillInCompletion(task.TaskToken, res), nil, nil
}

func (h *nexusTaskHandler) execute(task *workflowservice.PollNexusTaskQueueResponse) (*nexuspb.Response, *nexuspb.HandlerError, error) {
	log, handlerErr := h.loggerForTask(task)
	if handlerErr != nil {
		return nil, handlerErr, nil
	}
	nctx := &NexusOperationContext{
		Client:    h.client,
		TaskQueue: h.taskQueueName,
		Log:       log,
	}
	header := nexus.Header(task.GetRequest().GetHeader())
	if header == nil {
		header = nexus.Header{}
	}

	ctx, cancel, handlerErr := h.goContextForTask(nctx, header)
	if handlerErr != nil {
		return nil, handlerErr, nil
	}
	defer cancel()

	switch req := task.GetRequest().GetVariant().(type) {
	case *nexuspb.Request_StartOperation:
		return h.handleStartOperation(ctx, nctx, req.StartOperation, header)
	case *nexuspb.Request_CancelOperation:
		return h.handleCancelOperation(ctx, nctx, req.CancelOperation, header)
	default:
		return nil, nexusHandlerError(nexus.HandlerErrorTypeNotImplemented, "unknown request type"), nil
	}
}

func (h *nexusTaskHandler) handleStartOperation(ctx context.Context, nctx *NexusOperationContext, req *nexuspb.StartOperationRequest, header nexus.Header) (*nexuspb.Response, *nexuspb.HandlerError, error) {
	serializer := &payloadSerializer{
		converter: h.dataConverter,
		payload:   req.GetPayload(),
	}
	// Create a fake lazy value, Temporal server already converts Nexus content into payloads.
	input := nexus.NewLazyValue(
		serializer,
		&nexus.Reader{
			ReadCloser: emptyReaderNopCloser,
		},
	)
	// Ensure we don't pass nil values to handlers.
	callbackHeader := req.GetCallbackHeader()
	if callbackHeader == nil {
		callbackHeader = make(map[string]string)
	}
	startOptions := nexus.StartOperationOptions{
		RequestID:      req.RequestId,
		CallbackURL:    req.Callback,
		Header:         header,
		CallbackHeader: callbackHeader,
	}
	var opres nexus.HandlerStartOperationResult[any]
	var err error
	func() {
		defer func() {
			recovered := recover()
			if recovered != nil {
				var ok bool
				err, ok = recovered.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", recovered)
				}

				nctx.Log.Error("Panic captured while handling nexus task", tagStackTrace, string(debug.Stack()), tagError, err)
			}
		}()
		opres, err = h.nexusHandler.StartOperation(ctx, req.GetService(), req.GetOperation(), input, startOptions)
	}()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		var unsuccessfulOperationErr *nexus.UnsuccessfulOperationError
		if errors.As(err, &unsuccessfulOperationErr) {
			return &nexuspb.Response{
				Variant: &nexuspb.Response_StartOperation{
					StartOperation: &nexuspb.StartOperationResponse{
						Variant: &nexuspb.StartOperationResponse_OperationError{
							OperationError: &nexuspb.UnsuccessfulOperationError{
								OperationState: string(unsuccessfulOperationErr.State),
								Failure: &nexuspb.Failure{
									Message:  unsuccessfulOperationErr.Failure.Message,
									Metadata: unsuccessfulOperationErr.Failure.Metadata,
									Details:  unsuccessfulOperationErr.Failure.Details,
								},
							},
						},
					},
				},
			}, nil, nil
		}
		var handlerErr *nexus.HandlerError
		if errors.As(err, &handlerErr) {
			return nil, nexusHandlerErrorToProto(handlerErr), nil
		}
		// Default to internal error.
		return nil, h.internalError(err), nil
	}
	switch t := opres.(type) {
	case *nexus.HandlerStartOperationResultAsync:
		return &nexuspb.Response{
			Variant: &nexuspb.Response_StartOperation{
				StartOperation: &nexuspb.StartOperationResponse{
					Variant: &nexuspb.StartOperationResponse_AsyncSuccess{
						AsyncSuccess: &nexuspb.StartOperationResponse_Async{OperationId: t.OperationID},
					},
				},
			},
		}, nil, nil
	default:
		// *nexus.HandlerStartOperationResultSync is generic, we can't type switch unfortunately.
		value := reflect.ValueOf(t).Elem().FieldByName("Value").Interface()
		payload, err := h.dataConverter.ToPayload(value)
		if err != nil {
			return nil, h.internalError(fmt.Errorf("cannot convert nexus sync result: %w", err)), nil
		}
		return &nexuspb.Response{
			Variant: &nexuspb.Response_StartOperation{
				StartOperation: &nexuspb.StartOperationResponse{
					Variant: &nexuspb.StartOperationResponse_SyncSuccess{
						SyncSuccess: &nexuspb.StartOperationResponse_Sync{
							Payload: payload,
						},
					},
				},
			},
		}, nil, nil
	}
}

func (h *nexusTaskHandler) handleCancelOperation(ctx context.Context, nctx *NexusOperationContext, req *nexuspb.CancelOperationRequest, header nexus.Header) (*nexuspb.Response, *nexuspb.HandlerError, error) {
	cancelOptions := nexus.CancelOperationOptions{Header: header}
	var err error
	func() {
		defer func() {
			recovered := recover()
			if recovered != nil {
				var ok bool
				err, ok = recovered.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", recovered)
				}

				nctx.Log.Error("Panic captured while handling nexus task", tagStackTrace, string(debug.Stack()), tagError, err)
			}
		}()
		err = h.nexusHandler.CancelOperation(ctx, req.GetService(), req.GetOperation(), req.GetOperationId(), cancelOptions)
	}()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		var handlerErr *nexus.HandlerError
		if errors.As(err, &handlerErr) {
			return nil, nexusHandlerErrorToProto(handlerErr), nil
		}
		// Default to internal error.
		return nil, h.internalError(err), nil
	}

	return &nexuspb.Response{
		Variant: &nexuspb.Response_CancelOperation{
			CancelOperation: &nexuspb.CancelOperationResponse{},
		},
	}, nil, nil
}

func (h *nexusTaskHandler) internalError(err error) *nexuspb.HandlerError {
	h.logger.Error("error processing nexus task", "error", err)
	return nexusHandlerError(nexus.HandlerErrorTypeInternal, "internal error")
}

func (h *nexusTaskHandler) goContextForTask(nctx *NexusOperationContext, header nexus.Header) (context.Context, context.CancelFunc, *nexuspb.HandlerError) {
	// Associate the NexusOperationContext with the context.Context used to invoke operations.
	ctx := context.WithValue(context.Background(), nexusOperationContextKey, nctx)

	timeoutStr := header.Get(nexus.HeaderRequestTimeout)
	if timeoutStr != "" {
		timeout, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, nil, nexusHandlerError(nexus.HandlerErrorTypeBadRequest, "cannot parse request timeout")
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		return ctx, cancel, nil
	}

	return ctx, func() {}, nil
}

func (h *nexusTaskHandler) loggerForTask(response *workflowservice.PollNexusTaskQueueResponse) (log.Logger, *nexuspb.HandlerError) {
	var service, operation string

	switch req := response.GetRequest().GetVariant().(type) {
	case *nexuspb.Request_StartOperation:
		service = req.StartOperation.Service
		operation = req.StartOperation.Operation
	case *nexuspb.Request_CancelOperation:
		service = req.CancelOperation.Service
		operation = req.CancelOperation.Operation
	default:
		return nil, nexusHandlerError(nexus.HandlerErrorTypeNotImplemented, "unknown request type")
	}

	return log.With(h.logger,
		tagNexusService, service,
		tagNexusOperation, operation,
	), nil
}

func (h *nexusTaskHandler) metricsHandlerForTask(response *workflowservice.PollNexusTaskQueueResponse) (metrics.Handler, *nexuspb.HandlerError) {
	var service, operation string

	switch req := response.GetRequest().GetVariant().(type) {
	case *nexuspb.Request_StartOperation:
		service = req.StartOperation.Service
		operation = req.StartOperation.Operation
	case *nexuspb.Request_CancelOperation:
		service = req.CancelOperation.Service
		operation = req.CancelOperation.Operation
	default:
		return nil, &nexuspb.HandlerError{
			ErrorType: string(nexus.HandlerErrorTypeNotImplemented),
			Failure: &nexuspb.Failure{
				Message: "unknown request type",
			},
		}
	}

	return h.metricsHandler.WithTags(metrics.NexusTags(service, operation, h.taskQueueName)), nil
}

func (h *nexusTaskHandler) fillInCompletion(taskToken []byte, res *nexuspb.Response) *workflowservice.RespondNexusTaskCompletedRequest {
	return &workflowservice.RespondNexusTaskCompletedRequest{
		Identity:  h.identity,
		Namespace: h.namespace,
		TaskToken: taskToken,
		Response:  res,
	}
}

func (h *nexusTaskHandler) fillInFailure(taskToken []byte, err *nexuspb.HandlerError) *workflowservice.RespondNexusTaskFailedRequest {
	return &workflowservice.RespondNexusTaskFailedRequest{
		Identity:  h.identity,
		Namespace: h.namespace,
		TaskToken: taskToken,
		Error:     err,
	}
}

// payloadSerializer is a fake nexus Serializer that uses a data converter to read from an embedded payload instead of
// using the given nexus.Context. Supports only Deserialize.
type payloadSerializer struct {
	converter converter.DataConverter
	payload   *common.Payload
}

func (p *payloadSerializer) Deserialize(_ *nexus.Content, v any) error {
	return p.converter.FromPayload(p.payload, v)
}

func (p *payloadSerializer) Serialize(v any) (*nexus.Content, error) {
	panic("unimplemented") // not used - operation outputs are directly serialized to payload.
}

var emptyReaderNopCloser = io.NopCloser(bytes.NewReader([]byte{}))
