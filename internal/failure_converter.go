package internal

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"

	"go.temporal.io/sdk/converter"
)

var defaultFailureConverter = NewDefaultFailureConverter(DefaultFailureConverterOptions{})

// GetDefaultFailureConverter returns the default failure converter used by Temporal.
//
// Exposed as: [go.temporal.io/sdk/temporal.GetDefaultFailureConverter]
func GetDefaultFailureConverter() converter.FailureConverter {
	return defaultFailureConverter
}

// DefaultFailureConverterOptions are optional parameters for DefaultFailureConverter creation.
//
// Exposed as: [go.temporal.io/sdk/temporal.DefaultFailureConverterOptions]
type DefaultFailureConverterOptions struct {
	// Optional: Sets DataConverter to customize serialization/deserialization of fields.
	//
	// default: Default data converter
	DataConverter converter.DataConverter

	// Optional: Whether to encode error messages and stack traces.
	//
	// default: false
	EncodeCommonAttributes bool
}

// DefaultFailureConverter seralizes errors with the option to encode common parameters under Failure.EncodedAttributes
//
// Exposed as: [go.temporal.io/sdk/temporal.DefaultFailureConverter]
type DefaultFailureConverter struct {
	dataConverter          converter.DataConverter
	encodeCommonAttributes bool
}

// NewDefaultFailureConverter creates new instance of DefaultFailureConverter.
//
// Exposed as: [go.temporal.io/sdk/temporal.NewDefaultFailureConverter]
func NewDefaultFailureConverter(opt DefaultFailureConverterOptions) *DefaultFailureConverter {
	if opt.DataConverter == nil {
		opt.DataConverter = converter.GetDefaultDataConverter()
	}
	return &DefaultFailureConverter{
		dataConverter:          opt.DataConverter,
		encodeCommonAttributes: opt.EncodeCommonAttributes,
	}
}

// ErrorToFailure converts an error to a Failure
func (dfc *DefaultFailureConverter) ErrorToFailure(err error) *failurepb.Failure {
	if err == nil {
		return nil
	}

	if fh, ok := err.(failureHolder); ok {
		if fh.failure() != nil {
			return fh.failure()
		}
	}

	failure := &failurepb.Failure{
		Source: "GoSDK",
	}

	if m, ok := err.(messenger); ok && m != nil {
		failure.Message = m.message()
	} else {
		failure.Message = err.Error()
	}

	switch err := err.(type) {
	case *ApplicationError:
		var delay *durationpb.Duration
		if err.nextRetryDelay != 0 {
			delay = durationpb.New(err.nextRetryDelay)
		}
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type:           err.errType,
			NonRetryable:   err.NonRetryable(),
			Details:        convertErrDetailsToPayloads(err.details, dfc.dataConverter),
			NextRetryDelay: delay,
			Category:       enumspb.ApplicationErrorCategory(err.Category()),
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
	case *CanceledError:
		failureInfo := &failurepb.CanceledFailureInfo{
			Details: convertErrDetailsToPayloads(err.details, dfc.dataConverter),
		}
		failure.FailureInfo = &failurepb.Failure_CanceledFailureInfo{CanceledFailureInfo: failureInfo}
	case *PanicError:
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type: getErrType(err),
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
		failure.StackTrace = err.StackTrace()
	case *workflowPanicError:
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type:         getErrType(&PanicError{}),
			NonRetryable: true,
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
		failure.StackTrace = err.StackTrace()
	case *TimeoutError:
		failureInfo := &failurepb.TimeoutFailureInfo{
			TimeoutType:          err.timeoutType,
			LastHeartbeatDetails: convertErrDetailsToPayloads(err.lastHeartbeatDetails, dfc.dataConverter),
		}
		failure.FailureInfo = &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: failureInfo}
	case *TerminatedError:
		failureInfo := &failurepb.TerminatedFailureInfo{}
		failure.FailureInfo = &failurepb.Failure_TerminatedFailureInfo{TerminatedFailureInfo: failureInfo}
	case *ServerError:
		failureInfo := &failurepb.ServerFailureInfo{
			NonRetryable: err.nonRetryable,
		}
		failure.FailureInfo = &failurepb.Failure_ServerFailureInfo{ServerFailureInfo: failureInfo}
	case *ActivityError:
		failureInfo := &failurepb.ActivityFailureInfo{
			ScheduledEventId: err.scheduledEventID,
			StartedEventId:   err.startedEventID,
			Identity:         err.identity,
			ActivityType:     err.activityType,
			ActivityId:       err.activityID,
			RetryState:       err.retryState,
		}
		failure.FailureInfo = &failurepb.Failure_ActivityFailureInfo{ActivityFailureInfo: failureInfo}
	case *ChildWorkflowExecutionError:
		failureInfo := &failurepb.ChildWorkflowExecutionFailureInfo{
			Namespace: err.namespace,
			WorkflowExecution: &commonpb.WorkflowExecution{
				WorkflowId: err.workflowID,
				RunId:      err.runID,
			},
			WorkflowType:     &commonpb.WorkflowType{Name: err.workflowType},
			InitiatedEventId: err.initiatedEventID,
			StartedEventId:   err.startedEventID,
			RetryState:       err.retryState,
		}
		failure.FailureInfo = &failurepb.Failure_ChildWorkflowExecutionFailureInfo{ChildWorkflowExecutionFailureInfo: failureInfo}
	case *NexusOperationError:
		var token = err.OperationToken
		failureInfo := &failurepb.NexusOperationFailureInfo{
			ScheduledEventId: err.ScheduledEventID,
			Endpoint:         err.Endpoint,
			Service:          err.Service,
			Operation:        err.Operation,
			OperationId:      token,
			OperationToken:   token,
		}
		failure.FailureInfo = &failurepb.Failure_NexusOperationExecutionFailureInfo{NexusOperationExecutionFailureInfo: failureInfo}
	case *nexus.HandlerError:
		// if err.OriginalFailure != nil {
		// 	f, err := nexusFailureToAPIFailure(*err.OriginalFailure, true)
		// 	if err == nil {
		// 		return f
		// 	}
		// }
		var retryBehavior enumspb.NexusHandlerErrorRetryBehavior
		switch err.RetryBehavior {
		case nexus.HandlerErrorRetryBehaviorRetryable:
			retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE
		case nexus.HandlerErrorRetryBehaviorNonRetryable:
			retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_NON_RETRYABLE
		}
		failureInfo := &failurepb.NexusHandlerFailureInfo{
			Type:          string(err.Type),
			RetryBehavior: retryBehavior,
		}
		failure.FailureInfo = &failurepb.Failure_NexusHandlerFailureInfo{NexusHandlerFailureInfo: failureInfo}
		failure.StackTrace = err.StackTrace
		if len(err.Message) > 0 {
			failure.Message = err.Message
		}
	default: // All unknown errors are considered to be retryable ApplicationFailureInfo.
		failureInfo := &failurepb.ApplicationFailureInfo{
			Type:         getErrType(err),
			NonRetryable: false,
		}
		failure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: failureInfo}
	}

	failure.Cause = dfc.ErrorToFailure(errors.Unwrap(err))

	if dfc.encodeCommonAttributes {
		err := converter.EncodeCommonFailureAttributes(dfc.dataConverter, failure)
		if err != nil {
			panic(err)
		}
	}
	return failure
}

// FailureToError converts an Failure to an error
func (dfc *DefaultFailureConverter) FailureToError(failure *failurepb.Failure) error {
	if failure == nil {
		return nil
	}
	// Copy the original future to pass to the failureHolder
	originalFailure := proto.Clone(failure).(*failurepb.Failure)
	converter.DecodeCommonFailureAttributes(dfc.dataConverter, failure)

	message := failure.GetMessage()
	stackTrace := failure.GetStackTrace()
	var err error

	if failure.GetApplicationFailureInfo() != nil {
		applicationFailureInfo := failure.GetApplicationFailureInfo()
		details := newEncodedValues(applicationFailureInfo.GetDetails(), dfc.dataConverter)
		switch applicationFailureInfo.GetType() {
		case getErrType(&PanicError{}):
			err = newPanicError(message, stackTrace)
		default:
			var nextRetryDelay time.Duration
			if delay := applicationFailureInfo.GetNextRetryDelay(); delay != nil {
				nextRetryDelay = delay.AsDuration()
			}
			err = NewApplicationErrorWithOptions(
				message,
				applicationFailureInfo.GetType(),
				ApplicationErrorOptions{
					NonRetryable:   applicationFailureInfo.GetNonRetryable(),
					Cause:          dfc.FailureToError(failure.GetCause()),
					Details:        []interface{}{details},
					NextRetryDelay: nextRetryDelay,
					Category:       ApplicationErrorCategory(applicationFailureInfo.GetCategory()),
				},
			)
		}
	} else if failure.GetCanceledFailureInfo() != nil {
		details := newEncodedValues(failure.GetCanceledFailureInfo().GetDetails(), dfc.dataConverter)
		err = NewCanceledErrorWithOptions(
			CanceledErrorOptions{
				Message: message,
				Details: []interface{}{details},
			},
		)
	} else if failure.GetTimeoutFailureInfo() != nil {
		timeoutFailureInfo := failure.GetTimeoutFailureInfo()
		lastHeartbeatDetails := newEncodedValues(timeoutFailureInfo.GetLastHeartbeatDetails(), dfc.dataConverter)
		err = NewTimeoutError(
			message,
			timeoutFailureInfo.GetTimeoutType(),
			dfc.FailureToError(failure.GetCause()),
			lastHeartbeatDetails)
	} else if failure.GetTerminatedFailureInfo() != nil {
		err = newTerminatedError()
	} else if failure.GetServerFailureInfo() != nil {
		err = NewServerError(message, failure.GetServerFailureInfo().GetNonRetryable(), dfc.FailureToError(failure.GetCause()))
	} else if failure.GetResetWorkflowFailureInfo() != nil {
		err = NewApplicationError(message, "", true, dfc.FailureToError(failure.GetCause()), failure.GetResetWorkflowFailureInfo().GetLastHeartbeatDetails())
	} else if failure.GetActivityFailureInfo() != nil {
		activityTaskInfoFailure := failure.GetActivityFailureInfo()
		err = NewActivityError(
			activityTaskInfoFailure.GetScheduledEventId(),
			activityTaskInfoFailure.GetStartedEventId(),
			activityTaskInfoFailure.GetIdentity(),
			activityTaskInfoFailure.GetActivityType(),
			activityTaskInfoFailure.GetActivityId(),
			activityTaskInfoFailure.GetRetryState(),
			dfc.FailureToError(failure.GetCause()),
		)
	} else if failure.GetChildWorkflowExecutionFailureInfo() != nil {
		childWorkflowExecutionFailureInfo := failure.GetChildWorkflowExecutionFailureInfo()
		err = NewChildWorkflowExecutionError(
			childWorkflowExecutionFailureInfo.GetNamespace(),
			childWorkflowExecutionFailureInfo.GetWorkflowExecution().GetWorkflowId(),
			childWorkflowExecutionFailureInfo.GetWorkflowExecution().GetRunId(),
			childWorkflowExecutionFailureInfo.GetWorkflowType().GetName(),
			childWorkflowExecutionFailureInfo.GetInitiatedEventId(),
			childWorkflowExecutionFailureInfo.GetStartedEventId(),
			childWorkflowExecutionFailureInfo.GetRetryState(),
			dfc.FailureToError(failure.GetCause()),
		)
	} else if info := failure.GetNexusOperationExecutionFailureInfo(); info != nil {
		token := info.GetOperationToken()
		if token == "" {
			//lint:ignore SA1019 ignore deprecated old operation id
			token = info.GetOperationId()
		}
		err = &NexusOperationError{
			Message:          failure.Message,
			Cause:            dfc.FailureToError(failure.GetCause()),
			Failure:          originalFailure,
			ScheduledEventID: info.GetScheduledEventId(),
			Endpoint:         info.GetEndpoint(),
			Service:          info.GetService(),
			Operation:        info.GetOperation(),
			OperationToken:   token,
		}
	} else if info := failure.GetNexusHandlerFailureInfo(); info != nil {
		var retryBehavior nexus.HandlerErrorRetryBehavior
		switch info.RetryBehavior {
		case enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE:
			retryBehavior = nexus.HandlerErrorRetryBehaviorRetryable
		case enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_NON_RETRYABLE:
			retryBehavior = nexus.HandlerErrorRetryBehaviorNonRetryable
		}
		// TODO(quinn): Check message for legacy format and not pass if so
		cause := dfc.FailureToError(failure.GetCause())
		typ := nexus.HandlerErrorType(info.Type)
		if strings.HasPrefix(message, fmt.Sprintf("handler error (%s)", typ)) {
			message = ""
		}
		originalFailure, terr := TemporalFailureToNexusFailure(failure)
		if terr != nil {
			originalFailure = nil
		}
		err = &nexus.HandlerError{
			Type:            typ,
			Message:         message,
			StackTrace:      stackTrace,
			Cause:           cause,
			RetryBehavior:   retryBehavior,
			OriginalFailure: originalFailure,
		}
	}

	if err == nil {
		// All unknown types are considered to be retryable ApplicationError.
		err = NewApplicationError(message, "", false, dfc.FailureToError(failure.GetCause()))
	}

	if fh, ok := err.(failureHolder); ok {
		fh.setFailure(originalFailure)
	}

	return err
}

// TemporalFailureToNexusFailure converts an API proto Failure to a Nexus SDK Failure setting the metadata "type" field to
// the proto fullname of the temporal API Failure message or the standard Nexus SDK failure types.
// Returns an error if the failure cannot be converted.
// Mutates the failure temporarily, unsetting the Message field to avoid duplicating the information in the serialized
// failure. Mutating was chosen over cloning for performance reasons since this function may be called frequently.
func TemporalFailureToNexusFailure(failure *failurepb.Failure) (*nexus.Failure, error) {
	// Check type and handle Nexus Failure
	message := failure.Message
	failure.Message = ""
	failure.StackTrace = ""
	b, err := protojson.Marshal(failure)
	if err != nil {
		return nil, err
	}
	return &nexus.Failure{
		Message:  message,
		Metadata: nexusFailureMetadata,
		Details:  b,
	}, nil
	// var causep *nexus.Failure
	// if failure.GetCause() != nil {
	// 	var cause nexus.Failure
	// 	var err error
	// 	cause, err = TemporalFailureToNexusFailure(failure.GetCause())
	// 	if err != nil {
	// 		return nexus.Failure{}, err
	// 	}
	// 	causep = &cause
	// }

	// switch info := failure.GetFailureInfo().(type) {
	// case *failurepb.Failure_NexusSdkOperationFailureInfo:
	// 	var encodedAttributes string
	// 	if failure.EncodedAttributes != nil {
	// 		b, err := protojson.Marshal(failure.EncodedAttributes)
	// 		if err != nil {
	// 			return nexus.Failure{}, fmt.Errorf("failed to deserialize OperationError attributes: %w", err)
	// 		}
	// 		encodedAttributes = base64.StdEncoding.EncodeToString(b)
	// 	}
	// 	operationError := serializedOperationError{
	// 		State:             info.NexusSdkOperationFailureInfo.GetState(),
	// 		EncodedAttributes: encodedAttributes,
	// 	}

	// 	details, err := json.Marshal(operationError)
	// 	if err != nil {
	// 		return nexus.Failure{}, err
	// 	}
	// 	return nexus.Failure{
	// 		Message:    failure.GetMessage(),
	// 		StackTrace: failure.GetStackTrace(),
	// 		Metadata: map[string]string{
	// 			"type": "nexus.OperationError",
	// 		},
	// 		Details: details,
	// 		Cause:   causep,
	// 	}, nil
	// case *failurepb.Failure_NexusHandlerFailureInfo:
	// 	var encodedAttributes string
	// 	if failure.EncodedAttributes != nil {
	// 		b, err := protojson.Marshal(failure.EncodedAttributes)
	// 		if err != nil {
	// 			return nexus.Failure{}, fmt.Errorf("failed to deserialize HandlerError attributes: %w", err)
	// 		}
	// 		encodedAttributes = base64.StdEncoding.EncodeToString(b)
	// 	}
	// 	var retryableOverride *bool
	// 	switch info.NexusHandlerFailureInfo.GetRetryBehavior() {
	// 	case enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE:
	// 		val := true
	// 		retryableOverride = &val
	// 	case enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_NON_RETRYABLE:
	// 		val := false
	// 		retryableOverride = &val
	// 	}

	// 	handlerError := serializedHandlerError{
	// 		Type:              info.NexusHandlerFailureInfo.GetType(),
	// 		RetryableOverride: retryableOverride,
	// 		EncodedAttributes: encodedAttributes,
	// 	}

	// 	details, err := json.Marshal(handlerError)
	// 	if err != nil {
	// 		return nexus.Failure{}, err
	// 	}
	// 	return nexus.Failure{
	// 		Message:    failure.GetMessage(),
	// 		StackTrace: failure.GetStackTrace(),
	// 		Metadata: map[string]string{
	// 			"type": "nexus.HandlerError",
	// 		},
	// 		Details: details,
	// 		Cause:   causep,
	// 	}, nil
	// case *failurepb.Failure_NexusSdkFailureErrorInfo:
	// 	return nexus.Failure{
	// 		Message:    failure.GetMessage(),
	// 		StackTrace: failure.GetStackTrace(),
	// 		Metadata:   info.NexusSdkFailureErrorInfo.GetMetadata(),
	// 		Details:    info.NexusSdkFailureErrorInfo.GetDetails(),
	// 		Cause:      causep,
	// 	}, nil
	// }
	// // Unset message and stack trace so it's not serialized in the details.
	// var message string
	// message, failure.Message = failure.Message, ""
	// var stackTrace string
	// stackTrace, failure.StackTrace = failure.StackTrace, ""

	// data, err := protojson.Marshal(failure)
	// failure.Message = message
	// failure.StackTrace = stackTrace
	// if err != nil {
	// 	return nexus.Failure{}, err
	// }

	// return nexus.Failure{
	// 	Message:    failure.GetMessage(),
	// 	StackTrace: failure.GetStackTrace(),
	// 	Metadata: map[string]string{
	// 		"type": failureTypeString,
	// 	},
	// 	Details: data,
	// 	Cause:   causep,
	// }, nil
}

// NexusFailureToTemporalFailure converts a Nexus Failure to an API proto Failure.

// If the failure metadata "type" field is set to the fullname of the temporal API Failure message, the failure is
// reconstructed using protojson.Unmarshal on the failure details field. Otherwise, the failure is reconstructed
// based on the known Nexus SDK failure types.
// Returns an error if the failure cannot be converted.
// nolint:revive // cognitive-complexity is high but justified to keep each case together
func NexusFailureToTemporalFailure(f nexus.Failure) (*failurepb.Failure, error) {
	if f.Metadata != nil {
		var apiFailure failurepb.Failure
		if err := protojson.Unmarshal(f.Details, &apiFailure); err != nil {
			return nil, err
		}
		// Restore these fields as they are not included in the marshalled failure.
		apiFailure.Message = f.Message
		apiFailure.StackTrace = f.StackTrace
		return &apiFailure, nil
	}
	return nil, nil

	// if f.Metadata != nil {
	// 	switch f.Metadata["type"] {
	// 	case failureTypeString:
	// 		if err := protojson.Unmarshal(f.Details, apiFailure); err != nil {
	// 			return nil, err
	// 		}
	// 		// Restore these fields as they are not included in the marshalled failure.
	// 		apiFailure.Message = f.Message
	// 		apiFailure.StackTrace = f.StackTrace

	// 	case "nexus.OperationError":
	// 		var se serializedOperationError
	// 		err := json.Unmarshal(f.Details, &se)
	// 		if err != nil {
	// 			return nil, fmt.Errorf("failed to deserialize OperationError: %w", err)
	// 		}
	// 		apiFailure.FailureInfo = &failurepb.Failure_NexusSdkOperationFailureInfo{
	// 			NexusSdkOperationFailureInfo: &failurepb.NexusSDKOperationFailureInfo{
	// 				State: se.State,
	// 			},
	// 		}
	// 		if len(se.EncodedAttributes) > 0 {
	// 			decoded, err := base64.StdEncoding.DecodeString(se.EncodedAttributes)
	// 			if err != nil {
	// 				return nil, fmt.Errorf("failed to decode base64 OperationError attributes: %w", err)
	// 			}
	// 			apiFailure.EncodedAttributes = &commonpb.Payload{}
	// 			if err := protojson.Unmarshal(decoded, apiFailure.EncodedAttributes); err != nil {
	// 				return nil, fmt.Errorf("failed to deserialize OperationError attributes: %w", err)
	// 			}
	// 		}

	// 	case "nexus.HandlerError":
	// 		var se serializedHandlerError
	// 		err := json.Unmarshal(f.Details, &se)
	// 		if err != nil {
	// 			return nil, fmt.Errorf("failed to deserialize HandlerError: %w", err)
	// 		}
	// 		var retryBehavior enumspb.NexusHandlerErrorRetryBehavior
	// 		if se.RetryableOverride == nil {
	// 			retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_UNSPECIFIED
	// 		} else if *se.RetryableOverride {
	// 			retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE
	// 		} else {
	// 			retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_NON_RETRYABLE
	// 		}
	// 		apiFailure.FailureInfo = &failurepb.Failure_NexusHandlerFailureInfo{
	// 			NexusHandlerFailureInfo: &failurepb.NexusHandlerFailureInfo{
	// 				Type:          se.Type,
	// 				RetryBehavior: retryBehavior,
	// 			},
	// 		}
	// 		if len(se.EncodedAttributes) > 0 {
	// 			decoded, err := base64.StdEncoding.DecodeString(se.EncodedAttributes)
	// 			if err != nil {
	// 				return nil, fmt.Errorf("failed to decode base64 HandlerError attributes: %w", err)
	// 			}
	// 			apiFailure.EncodedAttributes = &commonpb.Payload{}
	// 			if err := protojson.Unmarshal(decoded, apiFailure.EncodedAttributes); err != nil {
	// 				return nil, fmt.Errorf("failed to deserialize HandlerError attributes: %w", err)
	// 			}
	// 		}
	// 	default:
	// 		apiFailure.FailureInfo = &failurepb.Failure_NexusSdkFailureErrorInfo{
	// 			NexusSdkFailureErrorInfo: &failurepb.NexusSDKFailureErrorFailureInfo{
	// 				Metadata: f.Metadata,
	// 				Details:  f.Details,
	// 			},
	// 		}
	// 	}
	// } else if len(f.Details) > 0 {
	// 	apiFailure.FailureInfo = &failurepb.Failure_NexusSdkFailureErrorInfo{
	// 		NexusSdkFailureErrorInfo: &failurepb.NexusSDKFailureErrorFailureInfo{
	// 			Details: f.Details,
	// 		},
	// 	}
	// }

	// if f.Cause != nil {
	// 	var err error
	// 	apiFailure.Cause, err = NexusFailureToTemporalFailure(*f.Cause)
	// 	if err != nil {
	// 		return nil, err
	// 	}

	// }
	// return apiFailure, nil
}
