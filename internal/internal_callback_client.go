package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	callbackpb "go.temporal.io/api/callback/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/durationpb"
)

const pollCallbackTimeout = 60 * time.Second

type (
	// ClientCompleteNexusOperationOptions contains configuration parameters for completing an async Nexus operation.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CompleteNexusOperationOptions]
	ClientCompleteNexusOperationOptions struct {
		// ID - The unique identifier of the callback within its namespace.
		// If not set, a UUID will be generated.
		//
		// Optional.
		ID string
		// ScheduleToCloseTimeout - Total time allowed for the callback to complete.
		//
		// Optional: Defaults to server default.
		ScheduleToCloseTimeout time.Duration
		// TypedSearchAttributes - Search Attributes attached to the callback.
		//
		// Optional: default to none.
		TypedSearchAttributes SearchAttributes
	}

	// ClientCallbackExecutionHandle represents a running or completed standalone callback execution.
	// It can be used to wait for completion, describe, or terminate the callback.
	//
	// Methods may be added to this interface; implementing it directly is discouraged.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CallbackExecutionHandle]
	ClientCallbackExecutionHandle interface {
		// GetID returns the callback ID.
		GetID() string
		// Get waits until the callback finishes. Returns nil on success, or the failure as an error.
		Get(ctx context.Context) error
		// Describe returns detailed information about the callback execution.
		Describe(ctx context.Context, options ClientDescribeCallbackOptions) (*ClientCallbackExecutionDescription, error)
		// Terminate terminates the callback.
		Terminate(ctx context.Context, options ClientTerminateCallbackOptions) error
	}

	// ClientDescribeCallbackOptions contains options for ClientCallbackExecutionHandle.Describe call.
	// For future compatibility, currently unused.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.DescribeCallbackOptions]
	ClientDescribeCallbackOptions struct{}

	// ClientCancelCallbackOptions contains options for ClientCallbackExecutionHandle.Cancel call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CancelCallbackOptions]
	ClientCancelCallbackOptions struct {
		// Reason is optional description of the reason for cancellation.
		Reason string
	}

	// ClientTerminateCallbackOptions contains options for ClientCallbackExecutionHandle.Terminate call.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.TerminateCallbackOptions]
	ClientTerminateCallbackOptions struct {
		// Reason is optional description of the reason for termination.
		Reason string
	}

	// ClientCallbackExecutionInfo contains information about a callback execution.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CallbackExecutionInfo]
	ClientCallbackExecutionInfo struct {
		CallbackID            string
		State                 enumspb.CallbackState
		CreateTime            time.Time
		CloseTime             time.Time
		TypedSearchAttributes SearchAttributes
		StateTransitionCount  int64
	}

	// ClientCallbackExecutionDescription contains detailed information about a callback execution.
	// This is returned by ClientCallbackExecutionHandle.Describe.
	//
	// NOTE: Experimental
	//
	// Exposed as: [go.temporal.io/sdk/client.CallbackExecutionDescription]
	ClientCallbackExecutionDescription struct {
		ClientCallbackExecutionInfo
		// Raw PB message this struct was built from.
		RawExecutionInfo        *callbackpb.CallbackExecutionInfo
		Callback                *commonpb.Callback
		Attempt                 int32
		LastAttemptCompleteTime  time.Time
		NextAttemptScheduleTime time.Time
		BlockedReason           string
		ScheduleToCloseTimeout  time.Duration
		failureConverter        converter.FailureConverter
		inboundPayloadVisitor   PayloadVisitor
	}

	// clientCallbackHandleImpl is the default implementation of ClientCallbackExecutionHandle.
	clientCallbackHandleImpl struct {
		client     *WorkflowClient
		id         string
		pollResult *ClientPollCallbackResultOutput
	}
)

// GetLastFailure returns the last attempt failure of the callback execution, using the failure converter of the client used to
// make the Describe call. Returns nil if there was no failure.
func (d *ClientCallbackExecutionDescription) GetLastFailure() error {
	failure := d.RawExecutionInfo.GetLastAttemptFailure()
	if failure == nil {
		return nil
	}
	if err := visitProtoPayloads(context.Background(), d.inboundPayloadVisitor, failure); err != nil {
		return err
	}
	return d.failureConverter.FailureToError(failure)
}

func (h *clientCallbackHandleImpl) GetID() string {
	return h.id
}

func (h *clientCallbackHandleImpl) Get(ctx context.Context) error {
	if h.pollResult != nil {
		return h.pollResult.Error
	}
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}

	for {
		resp, err := h.client.interceptor.PollCallbackResult(ctx, &ClientPollCallbackResultInput{
			CallbackID: h.id,
		})
		if err != nil {
			return err
		}
		if resp != nil {
			h.pollResult = resp
			return resp.Error
		}
	}
}

func (h *clientCallbackHandleImpl) Describe(ctx context.Context, options ClientDescribeCallbackOptions) (*ClientCallbackExecutionDescription, error) {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	out, err := h.client.interceptor.DescribeCallback(ctx, &ClientDescribeCallbackInput{
		CallbackID: h.id,
	})
	if err != nil {
		return nil, err
	}
	return out.Description, nil
}

func (h *clientCallbackHandleImpl) Terminate(ctx context.Context, options ClientTerminateCallbackOptions) error {
	if err := h.client.ensureInitialized(ctx); err != nil {
		return err
	}
	return h.client.interceptor.TerminateCallback(ctx, &ClientTerminateCallbackInput{
		CallbackID: h.id,
		Reason:     options.Reason,
	})
}

// CompleteNexusOperation completes an async Nexus operation with a success result.
func (wc *WorkflowClient) CompleteNexusOperation(ctx context.Context, callbackToken string, result any, options ClientCompleteNexusOperationOptions) error {
	handle, err := wc.StartCompleteNexusOperation(ctx, callbackToken, result, options)
	if err != nil {
		return err
	}
	return handle.Get(ctx)
}

// FailNexusOperation fails an async Nexus operation with an error.
func (wc *WorkflowClient) FailNexusOperation(ctx context.Context, callbackToken string, failure error, options ClientCompleteNexusOperationOptions) error {
	handle, err := wc.StartFailNexusOperation(ctx, callbackToken, failure, options)
	if err != nil {
		return err
	}
	return handle.Get(ctx)
}

// StartCompleteNexusOperation starts completing an async Nexus operation and returns a handle.
func (wc *WorkflowClient) StartCompleteNexusOperation(ctx context.Context, callbackToken string, result any, options ClientCompleteNexusOperationOptions) (ClientCallbackExecutionHandle, error) {
	if err := wc.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return wc.interceptor.ExecuteCallback(ctx, &ClientExecuteCallbackInput{
		Options:       &options,
		CallbackToken: callbackToken,
		Result:        result,
	})
}

// StartFailNexusOperation starts failing an async Nexus operation and returns a handle.
func (wc *WorkflowClient) StartFailNexusOperation(ctx context.Context, callbackToken string, failure error, options ClientCompleteNexusOperationOptions) (ClientCallbackExecutionHandle, error) {
	if err := wc.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return wc.interceptor.ExecuteCallback(ctx, &ClientExecuteCallbackInput{
		Options:       &options,
		CallbackToken: callbackToken,
		Failure:       failure,
	})
}

func (wc *WorkflowClient) GetCallbackExecutionHandle(options ClientGetCallbackExecutionHandleOptions) ClientCallbackExecutionHandle {
	return wc.interceptor.GetCallbackExecutionHandle((*ClientGetCallbackExecutionHandleInput)(&options))
}

// ClientGetCallbackExecutionHandleOptions contains input for GetCallbackExecutionHandle call.
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/client.GetCallbackExecutionHandleOptions]
type ClientGetCallbackExecutionHandleOptions struct {
	CallbackID string
}

func (w *workflowClientInterceptor) ExecuteCallback(
	ctx context.Context,
	in *ClientExecuteCallbackInput,
) (ClientCallbackExecutionHandle, error) {
	ctx = contextWithNewHeader(ctx)
	dataConverter := WithContext(ctx, w.client.dataConverter)
	if dataConverter == nil {
		dataConverter = converter.GetDefaultDataConverter()
	}

	callbackID := in.Options.ID
	if callbackID == "" {
		callbackID = uuid.NewString()
	}

	callbackProto := &commonpb.Callback{
		Variant: &commonpb.Callback_Nexus_{
			Nexus: &commonpb.Callback_Nexus{
				Url:    "temporal://system",
				Header: map[string]string{"Temporal-Callback-Token": in.CallbackToken},
			},
		},
	}

	searchAttrs, err := serializeTypedSearchAttributes(in.Options.TypedSearchAttributes.GetUntypedValues())
	if err != nil {
		return nil, err
	}

	// Build the completion proto from the explicit Result or Failure field.
	completion := &callbackpb.CallbackExecutionCompletion{}
	if in.Failure != nil {
		completion.Result = &callbackpb.CallbackExecutionCompletion_Failure{
			Failure: w.client.failureConverter.ErrorToFailure(in.Failure),
		}
	} else {
		payload, err := dataConverter.ToPayload(in.Result)
		if err != nil {
			return nil, err
		}
		completion.Result = &callbackpb.CallbackExecutionCompletion_Success{
			Success: payload,
		}
	}

	request := &workflowservice.StartCallbackExecutionRequest{
		Namespace:              w.client.namespace,
		Identity:               w.client.identity,
		RequestId:              uuid.NewString(),
		CallbackId:             callbackID,
		Callback:               callbackProto,
		ScheduleToCloseTimeout: durationpb.New(in.Options.ScheduleToCloseTimeout),
		SearchAttributes:       searchAttrs,
		Input: &workflowservice.StartCallbackExecutionRequest_Completion{
			Completion: completion,
		},
	}

	if err := visitProtoPayloads(ctx, w.client.outboundPayloadVisitor, request); err != nil {
		return nil, err
	}

	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	_, err = w.client.WorkflowService().StartCallbackExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}

	return &clientCallbackHandleImpl{
		client: w.client,
		id:     callbackID,
	}, nil
}

func (w *workflowClientInterceptor) GetCallbackExecutionHandle(
	in *ClientGetCallbackExecutionHandleInput,
) ClientCallbackExecutionHandle {
	return &clientCallbackHandleImpl{
		client: w.client,
		id:     in.CallbackID,
	}
}

func (w *workflowClientInterceptor) PollCallbackResult(
	ctx context.Context,
	in *ClientPollCallbackResultInput,
) (*ClientPollCallbackResultOutput, error) {
	request := &workflowservice.PollCallbackExecutionRequest{
		Namespace:  w.client.namespace,
		CallbackId: in.CallbackID,
	}

	var resp *workflowservice.PollCallbackExecutionResponse
	for resp.GetOutcome() == nil {
		grpcCtx, cancel := newGRPCContext(ctx, grpcLongPoll(true), grpcTimeout(pollCallbackTimeout), defaultGrpcRetryParameters(ctx))
		var err error
		resp, err = w.client.WorkflowService().PollCallbackExecution(grpcCtx, request)
		cancel()
		if err != nil {
			return nil, err
		}
	}

	if err := visitProtoPayloads(ctx, w.client.inboundPayloadVisitor, resp); err != nil {
		return nil, err
	}

	switch v := resp.GetOutcome().GetValue().(type) {
	case *callbackpb.CallbackExecutionOutcome_Success:
		return &ClientPollCallbackResultOutput{Error: nil}, nil
	case *callbackpb.CallbackExecutionOutcome_Failure:
		return &ClientPollCallbackResultOutput{Error: w.client.failureConverter.FailureToError(v.Failure)}, nil
	default:
		return nil, fmt.Errorf("unexpected callback outcome type: %T", v)
	}
}

func (w *workflowClientInterceptor) DescribeCallback(
	ctx context.Context,
	in *ClientDescribeCallbackInput,
) (*ClientDescribeCallbackOutput, error) {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.DescribeCallbackExecutionRequest{
		Namespace:  w.client.namespace,
		CallbackId: in.CallbackID,
	}
	resp, err := w.client.WorkflowService().DescribeCallbackExecution(grpcCtx, request)
	if err != nil {
		return nil, err
	}
	info := resp.GetInfo()
	if info == nil {
		return nil, fmt.Errorf("DescribeCallbackExecution response doesn't contain info")
	}

	if err := visitProtoPayloads(ctx, w.client.inboundPayloadVisitor, resp); err != nil {
		return nil, err
	}

	return &ClientDescribeCallbackOutput{
		Description: &ClientCallbackExecutionDescription{
			ClientCallbackExecutionInfo: ClientCallbackExecutionInfo{
				CallbackID:            info.CallbackId,
				State:                 info.State,
				CreateTime:            info.CreateTime.AsTime(),
				CloseTime:             info.CloseTime.AsTime(),
				TypedSearchAttributes: convertToTypedSearchAttributes(w.client.logger, info.SearchAttributes.GetIndexedFields()),
				StateTransitionCount:  info.StateTransitionCount,
			},
			RawExecutionInfo:        info,
			Callback:                info.Callback,
			Attempt:                 info.Attempt,
			LastAttemptCompleteTime:  info.LastAttemptCompleteTime.AsTime(),
			NextAttemptScheduleTime: info.NextAttemptScheduleTime.AsTime(),
			BlockedReason:           info.BlockedReason,
			ScheduleToCloseTimeout:  info.ScheduleToCloseTimeout.AsDuration(),
			failureConverter:        w.client.failureConverter,
			inboundPayloadVisitor:   w.client.inboundPayloadVisitor,
		},
	}, nil
}

func (w *workflowClientInterceptor) TerminateCallback(
	ctx context.Context,
	in *ClientTerminateCallbackInput,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()

	request := &workflowservice.TerminateCallbackExecutionRequest{
		Namespace:  w.client.namespace,
		CallbackId: in.CallbackID,
		Identity:   w.client.identity,
		RequestId:  uuid.NewString(),
		Reason:     in.Reason,
	}
	_, err := w.client.WorkflowService().TerminateCallbackExecution(grpcCtx, request)
	return err
}
