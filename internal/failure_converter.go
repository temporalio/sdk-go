package internal

import (
	"encoding/json"
	"errors"
	"fmt"
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
	} else if opErr, ok := err.(*nexus.OperationError); ok && opErr.OriginalFailure != nil {
		f, err := nexusFailureToTemporalFailure(*opErr.OriginalFailure)
		if err != nil {
			return nil
		}
		return f
	} else if opErr, ok := err.(*nexus.HandlerError); ok && opErr.OriginalFailure != nil {
		f, err := nexusFailureToTemporalFailure(*opErr.OriginalFailure)
		if err != nil {
			return nil
		}
		return f
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
		failure.Message = err.Message
	case *nexus.OperationError:
		failureInfo := &failurepb.NexusSDKOperationFailureInfo{
			State: string(err.State),
		}
		failure.FailureInfo = &failurepb.Failure_NexusSdkOperationFailureInfo{NexusSdkOperationFailureInfo: failureInfo}
		failure.Message = err.Message
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
		err = NewCanceledError(details)
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
		originalFailure, err := temporalFailureToNexusFailure(failure)
		if err != nil {
			// TODO
			panic("failed to convert Nexus SDK Operation Failure to Nexus Failure: " + err.Error())
		}
		err = &nexus.HandlerError{
			Message:         failure.Message,
			StackTrace:      failure.StackTrace,
			Type:            nexus.HandlerErrorType(info.Type),
			RetryBehavior:   retryBehavior,
			Cause:           dfc.FailureToError(failure.GetCause()),
			OriginalFailure: originalFailure,
		}
	} else if info := failure.GetNexusSdkOperationFailureInfo(); info != nil {
		originalFailure, err := temporalFailureToNexusFailure(failure)
		if err != nil {
			// TODO
			panic("failed to convert Nexus SDK Operation Failure to Nexus Failure: " + err.Error())
		}
		err = &nexus.OperationError{
			Message:         failure.Message,
			StackTrace:      failure.StackTrace,
			State:           nexus.OperationState(info.State),
			Cause:           dfc.FailureToError(failure.GetCause()),
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

type serializedOperationError struct {
	State string `json:"state,omitempty"`
	// Bytes as base64 encoded string.
	EncodedAttributes string `json:"encodedAttributes,omitempty"`
}

type serializedHandlerError struct {
	Type              string `json:"type,omitempty"`
	RetryableOverride *bool  `json:"retryableOverride,omitempty"`
	// Bytes as base64 encoded string.
	EncodedAttributes string `json:"encodedAttributes,omitempty"`
}

// nexusFailureToTemporalFailure converts a Nexus Failure to a Temporal API proto Failure.
func nexusFailureToTemporalFailure(f nexus.Failure) (*failurepb.Failure, error) {
	apiFailure := &failurepb.Failure{
		Message:    f.Message,
		StackTrace: f.StackTrace,
	}

	if f.Metadata != nil {
		switch f.Metadata["type"] {
		case failureTypeString:
			if err := protojson.Unmarshal(f.Details, apiFailure); err != nil {
				return nil, err
			}
			// Restore these fields as they are not included in the marshalled failure.
			// TODO: is this required? Unmarshal should not touch these fields.
			apiFailure.Message = f.Message
			apiFailure.StackTrace = f.StackTrace
			return apiFailure, nil
		case "nexus.OperationError":
			var se serializedOperationError
			err := json.Unmarshal(f.Details, &se)
			if err != nil {
				return nil, fmt.Errorf("failed to deserialize OperationError: %w", err)
			}
			apiFailure.FailureInfo = &failurepb.Failure_NexusSdkOperationFailureInfo{
				NexusSdkOperationFailureInfo: &failurepb.NexusSDKOperationFailureInfo{
					State: se.State,
				},
			}
			if err := protojson.Unmarshal([]byte(se.EncodedAttributes), apiFailure.EncodedAttributes); err != nil {
				return nil, fmt.Errorf("failed to deserialize OperationError attributes: %w", err)
			}
			if f.Cause != nil {
				apiFailure.Cause, err = nexusFailureToTemporalFailure(*f.Cause)
				if err != nil {
					return nil, err
				}
			}
			return apiFailure, nil
		case "nexus.HandlerError":
			var se serializedHandlerError
			err := json.Unmarshal(f.Details, &se)
			if err != nil {
				return nil, fmt.Errorf("failed to deserialize HandlerError: %w", err)
			}
			var retryBehavior enumspb.NexusHandlerErrorRetryBehavior
			if se.RetryableOverride == nil {
				retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_UNSPECIFIED
			} else if *se.RetryableOverride {
				retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE
			} else {
				retryBehavior = enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_NON_RETRYABLE
			}
			apiFailure.FailureInfo = &failurepb.Failure_NexusHandlerFailureInfo{
				NexusHandlerFailureInfo: &failurepb.NexusHandlerFailureInfo{
					Type:          se.Type,
					RetryBehavior: retryBehavior,
				},
			}
			if err := protojson.Unmarshal([]byte(se.EncodedAttributes), apiFailure.EncodedAttributes); err != nil {
				return nil, fmt.Errorf("failed to deserialize HandlerError attributes: %w", err)
			}
			if f.Cause != nil {
				apiFailure.Cause, err = nexusFailureToTemporalFailure(*f.Cause)
				if err != nil {
					return nil, err
				}
			}
			return apiFailure, nil
		}
	}

	// TODO: consider a special failurepb defintion for generic Nexus failures.
	payloads, err := nexusFailureMetadataToPayloads(f)
	if err != nil {
		return nil, err
	}
	apiFailure.FailureInfo = &failurepb.Failure_ApplicationFailureInfo{
		ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
			// Make up a type here, it's not part of the Nexus Failure spec.
			Type:    "NexusFailure",
			Details: payloads,
		},
	}
	return apiFailure, nil
}

func temporalFailureToNexusFailure(f *failurepb.Failure) (*nexus.Failure, error) {
	if f == nil {
		return nil, nil
	}
	nexusFailure := &nexus.Failure{
		Message:    f.Message,
		StackTrace: f.StackTrace,
	}

	if info := f.GetNexusHandlerFailureInfo(); info != nil {
		var retryableOverride *bool
		switch info.RetryBehavior {
		case enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_RETRYABLE:
			retryableOverride = new(bool)
			*retryableOverride = true
		case enumspb.NEXUS_HANDLER_ERROR_RETRY_BEHAVIOR_NON_RETRYABLE:
			retryableOverride = new(bool)
			*retryableOverride = false
		}
		encodedAttributes, err := protojson.Marshal(f.EncodedAttributes)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize HandlerError attributes: %w", err)
		}
		se := serializedHandlerError{
			Type:              info.Type,
			RetryableOverride: retryableOverride,
			EncodedAttributes: string(encodedAttributes),
		}
		details, err := json.Marshal(&se)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize HandlerError: %w", err)
		}
		nexusFailure.Metadata = map[string]string{"type": "nexus.HandlerError"}
		nexusFailure.Details = details

		if f.Cause != nil {
			nexusFailure.Cause, err = temporalFailureToNexusFailure(f.Cause)
			if err != nil {
				return nil, err
			}
		}
	} else if info := f.GetNexusSdkOperationFailureInfo(); info != nil {
		encodedAttributes, err := protojson.Marshal(f.EncodedAttributes)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize OperationError attributes: %w", err)
		}
		se := serializedOperationError{
			State:             info.State,
			EncodedAttributes: string(encodedAttributes),
		}
		details, err := json.Marshal(&se)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize OperationError: %w", err)
		}
		nexusFailure.Metadata = map[string]string{"type": "nexus.OperationError"}
		nexusFailure.Details = details

		if f.Cause != nil {
			nexusFailure.Cause, err = temporalFailureToNexusFailure(f.Cause)
			if err != nil {
				return nil, err
			}
		}
	} else {
		nexusFailure.Metadata = map[string]string{"type": failureTypeString}
		f.Message, f.StackTrace = "", ""
		details, err := protojson.Marshal(f)
		if err != nil {
			return nil, err
		}
		nexusFailure.Details = details
		// Restore these fields as they are not included in the marshalled failure.
		f.Message = nexusFailure.Message
		f.StackTrace = nexusFailure.StackTrace
	}

	return nexusFailure, nil
}
