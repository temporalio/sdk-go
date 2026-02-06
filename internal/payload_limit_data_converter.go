package internal

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

type PayloadLimitDataConverter struct {
	innerConverter converter.DataConverter
	errorLimits    *PayloadErrorLimits
	options        PayloadLimitDataConverterOptions
}

type PayloadLimitDataConverterOptions struct {
	Logger             log.Logger
	PayloadSizeWarning int
}

type PayloadSizeError struct {
	message string
}

func (e PayloadSizeError) Error() string {
	return e.message
}

type PayloadErrorLimits struct {
	PayloadSizeError int64
}

// NOTE: This func returns the data converter and a func callback allowing the setting of error limits after the converter has been created.
func NewPayloadLimitDataConverter(innerConverter converter.DataConverter, logger log.Logger) (converter.DataConverter, func(*PayloadErrorLimits)) {
	return NewPayloadLimitDataConverterWithOptions(
		innerConverter,
		logger,
		PayloadLimitOptions{},
	)
}

// NOTE: This func returns the data converter and a func callback allowing the setting of error limits after the converter has been created.
func NewPayloadLimitDataConverterWithOptions(innerConverter converter.DataConverter, logger log.Logger, options PayloadLimitOptions) (converter.DataConverter, func(*PayloadErrorLimits)) {
	payloadSizeWarning := 512 * kb
	if options.PayloadSizeWarning != 0 {
		payloadSizeWarning = options.PayloadSizeWarning
	}
	dataConverter := &PayloadLimitDataConverter{
		innerConverter: innerConverter,
		options: PayloadLimitDataConverterOptions{
			Logger:             logger,
			PayloadSizeWarning: payloadSizeWarning,
		},
	}
	return dataConverter, dataConverter.SetErrorLimits
}

func (converter *PayloadLimitDataConverter) SetErrorLimits(errorLimits *PayloadErrorLimits) {
	converter.errorLimits = errorLimits
}

func (converter *PayloadLimitDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := converter.innerConverter.ToPayload(value)
	if err != nil {
		return nil, err
	}
	payloads := &commonpb.Payloads{
		Payloads: []*commonpb.Payload{payload},
	}
	err = proxy.VisitPayloads(context.TODO(), payloads, proxy.VisitPayloadsOptions{
		Visitor: converter.checkPayloadSizeVisitor,
	})
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (converter *PayloadLimitDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	return converter.innerConverter.FromPayload(payload, valuePtr)
}

func (converter *PayloadLimitDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := converter.innerConverter.ToPayloads(value...)
	if err != nil {
		return nil, err
	}
	err = proxy.VisitPayloads(context.TODO(), payloads, proxy.VisitPayloadsOptions{
		Visitor: converter.checkPayloadSizeVisitor,
	})
	if err != nil {
		return nil, err
	}
	return payloads, nil
}

func (converter *PayloadLimitDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	return converter.innerConverter.FromPayloads(payloads, valuePtrs...)
}

func (converter *PayloadLimitDataConverter) ToString(input *commonpb.Payload) string {
	return converter.innerConverter.ToString(input)
}

func (converter *PayloadLimitDataConverter) ToStrings(input *commonpb.Payloads) []string {
	return converter.innerConverter.ToStrings(input)
}

func (converter *PayloadLimitDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	logger := converter.options.Logger
	if workflowEnvironmentAny := ctx.Value(workflowEnvironmentContextKey); workflowEnvironmentAny != nil {
		workflowInfo := workflowEnvironmentAny.(WorkflowEnvironment).WorkflowInfo()
		logger = log.With(logger,
			tagWorkflowType, workflowInfo.WorkflowType.Name,
			tagWorkflowID, workflowInfo.WorkflowExecution.ID,
			tagRunID, workflowInfo.WorkflowExecution.RunID)
	}

	innerConverter := converter.innerConverter
	if contextAwareInnerConverter, ok := converter.innerConverter.(ContextAware); ok {
		innerConverter = contextAwareInnerConverter.WithWorkflowContext(ctx)
	}

	if logger == converter.options.Logger && innerConverter == converter.innerConverter {
		return converter
	}

	return &PayloadLimitDataConverter{
		innerConverter: innerConverter,
		errorLimits:    converter.errorLimits,
		options: PayloadLimitDataConverterOptions{
			Logger:             logger,
			PayloadSizeWarning: converter.options.PayloadSizeWarning,
		},
	}
}

func (converter *PayloadLimitDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	logger := converter.options.Logger
	if activityEnvInterceptorAny := ctx.Value(activityEnvInterceptorContextKey); activityEnvInterceptorAny != nil {
		activityEnvInterceptorPtr := activityEnvInterceptorAny.(*activityEnvironmentInterceptor)
		if activityEnvPtr := activityEnvInterceptorPtr.env; activityEnvPtr != nil {
			if workflowType := activityEnvPtr.workflowType; workflowType != nil {
				logger = log.With(logger,
					tagWorkflowType, workflowType.Name)
			}
			logger = log.With(logger,
				tagWorkflowID, activityEnvPtr.workflowExecution.ID,
				tagRunID, activityEnvPtr.workflowExecution.RunID,
				tagActivityID, activityEnvPtr.activityID,
				tagAttempt, activityEnvPtr.attempt,
				tagActivityType, activityEnvPtr.activityType.Name)
		}
	}

	innerConverter := converter.innerConverter
	if contextAwareInnerConverter, ok := converter.innerConverter.(ContextAware); ok {
		innerConverter = contextAwareInnerConverter.WithContext(ctx)
	}

	if logger == converter.options.Logger && innerConverter == converter.innerConverter {
		return converter
	}

	return &PayloadLimitDataConverter{
		innerConverter: innerConverter,
		errorLimits:    converter.errorLimits,
		options: PayloadLimitDataConverterOptions{
			Logger:             logger,
			PayloadSizeWarning: converter.options.PayloadSizeWarning,
		},
	}
}

func (converter *PayloadLimitDataConverter) checkPayloadSizeVisitor(vpc *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	var totalSize int64
	for _, payload := range payloads {
		if payload != nil {
			totalSize += int64(payload.Size())
		}
	}
	if converter.errorLimits != nil && converter.errorLimits.PayloadSizeError > 0 && totalSize > converter.errorLimits.PayloadSizeError {
		return nil, PayloadSizeError{message: "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit."}
	}
	if converter.options.PayloadSizeWarning > 0 && totalSize > int64(converter.options.PayloadSizeWarning) && converter.options.Logger != nil {
		converter.options.Logger.Warn("[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
	}
	return payloads, nil
}
