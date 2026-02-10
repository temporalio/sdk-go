package internal

import (
	"context"
	"sync/atomic"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

// Options for when payload sizes exceed limits.
//
// Exposed as: [go.temporal.io/sdk/client.PayloadLimitOptions]
type PayloadLimitOptions struct {
	// The limit (in bytes) at which a payload size warning is logged.
	PayloadSizeWarning int
}

type payloadLimitDataConverter struct {
	innerConverter converter.DataConverter
	errorLimits    atomic.Pointer[payloadErrorLimits]
	options        payloadLimitDataConverterOptions
}

type payloadLimitDataConverterOptions struct {
	Logger             log.Logger
	PayloadSizeWarning int
}

type payloadSizeError struct {
	message string
}

func (e payloadSizeError) Error() string {
	return e.message
}

type payloadErrorLimits struct {
	PayloadSizeError int64
}

// NOTE: This func returns the data converter and a func callback allowing the setting of error limits after the converter has been created.
func newPayloadLimitDataConverter(innerConverter converter.DataConverter, logger log.Logger, options PayloadLimitOptions) (converter.DataConverter, func(*payloadErrorLimits)) {
	payloadSizeWarning := 512 * kb
	if options.PayloadSizeWarning != 0 {
		payloadSizeWarning = options.PayloadSizeWarning
	}
	dataConverter := &payloadLimitDataConverter{
		innerConverter: innerConverter,
		options: payloadLimitDataConverterOptions{
			Logger:             logger,
			PayloadSizeWarning: payloadSizeWarning,
		},
	}
	return dataConverter, dataConverter.SetErrorLimits
}

func (c *payloadLimitDataConverter) SetErrorLimits(errorLimits *payloadErrorLimits) {
	c.errorLimits.Store(errorLimits)
}

func (c *payloadLimitDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := c.innerConverter.ToPayload(value)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		err = c.checkPayloadsSize([]*commonpb.Payload{payload})
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func (c *payloadLimitDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	return c.innerConverter.FromPayload(payload, valuePtr)
}

func (c *payloadLimitDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := c.innerConverter.ToPayloads(value...)
	if err != nil {
		return nil, err
	}
	if payloads != nil {
		err = c.checkPayloadsSize(payloads.Payloads)
		if err != nil {
			return nil, err
		}
	}
	return payloads, nil
}

func (c *payloadLimitDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	return c.innerConverter.FromPayloads(payloads, valuePtrs...)
}

func (c *payloadLimitDataConverter) ToString(input *commonpb.Payload) string {
	return c.innerConverter.ToString(input)
}

func (c *payloadLimitDataConverter) ToStrings(input *commonpb.Payloads) []string {
	return c.innerConverter.ToStrings(input)
}

func (c *payloadLimitDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	innerConverter := c.innerConverter
	if contextAwareInnerConverter, ok := c.innerConverter.(ContextAware); ok {
		innerConverter = contextAwareInnerConverter.WithWorkflowContext(ctx)
	}

	newConverter := &payloadLimitDataConverter{
		innerConverter: innerConverter,
		options: payloadLimitDataConverterOptions{
			Logger:             GetLogger(ctx),
			PayloadSizeWarning: c.options.PayloadSizeWarning,
		},
	}
	newConverter.errorLimits.Store(c.errorLimits.Load())
	return newConverter
}

func (c *payloadLimitDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	logger := c.options.Logger
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

	innerConverter := c.innerConverter
	if contextAwareInnerConverter, ok := c.innerConverter.(ContextAware); ok {
		innerConverter = contextAwareInnerConverter.WithContext(ctx)
	}

	if logger == c.options.Logger && innerConverter == c.innerConverter {
		return c
	}

	newConverter := &payloadLimitDataConverter{
		innerConverter: innerConverter,
		options: payloadLimitDataConverterOptions{
			Logger:             logger,
			PayloadSizeWarning: c.options.PayloadSizeWarning,
		},
	}
	newConverter.errorLimits.Store(c.errorLimits.Load())
	return newConverter
}

func (c *payloadLimitDataConverter) checkPayloadsSize(payloads []*commonpb.Payload) error {
	var totalSize int64
	for _, payload := range payloads {
		if payload != nil {
			totalSize += int64(payload.Size())
		}
	}
	errorLimits := c.errorLimits.Load()
	if errorLimits != nil && errorLimits.PayloadSizeError > 0 && totalSize > errorLimits.PayloadSizeError {
		return payloadSizeError{message: "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit."}
	}
	if c.options.PayloadSizeWarning > 0 && totalSize > int64(c.options.PayloadSizeWarning) && c.options.Logger != nil {
		c.options.Logger.Warn("[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
	}
	return nil
}
