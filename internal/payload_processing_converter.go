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

type payloadSizeError struct {
	message string
	size    int64
	limit   int64
}

func (e payloadSizeError) Error() string {
	return e.message
}

type payloadErrorLimits struct {
	PayloadSizeError int64
}

// payloadProcessingDataConverter is a converter that wraps another data converter and applies post conversion operations
// to payloads, such as payload size limit logging and enforcement. Future operations may be added to create an internal
// centralized pipeline of transformations and validations in one converter.
type payloadProcessingDataConverter struct {
	converter.DataConverter
	errorLimits        atomic.Pointer[payloadErrorLimits]
	logger             log.Logger
	payloadSizeWarning int
}

// NOTE: This func returns the data converter and a func callback allowing the setting of error limits after the converter has been created.
func newPayloadProcessingDataConverter(innerConverter converter.DataConverter, logger log.Logger, options PayloadLimitOptions) (converter.DataConverter, func(*payloadErrorLimits)) {
	payloadSizeWarning := 512 * kb
	if options.PayloadSizeWarning != 0 {
		payloadSizeWarning = options.PayloadSizeWarning
	}
	dataConverter := &payloadProcessingDataConverter{
		DataConverter:      innerConverter,
		logger:             logger,
		payloadSizeWarning: payloadSizeWarning,
	}
	return dataConverter, dataConverter.SetErrorLimits
}

func (c *payloadProcessingDataConverter) SetErrorLimits(errorLimits *payloadErrorLimits) {
	c.errorLimits.Store(errorLimits)
}

func (c *payloadProcessingDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := c.DataConverter.ToPayload(value)
	if err != nil {
		return nil, err
	}
	// Other operations can be inserted here.

	// Size limit checks should remain the last operation before returning
	if payload != nil {
		err = c.checkPayloadsSize([]*commonpb.Payload{payload})
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func (c *payloadProcessingDataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := c.DataConverter.ToPayloads(value...)
	if err != nil {
		return nil, err
	}
	// Other operations can be inserted here.

	// Size limit checks should remain the last operation before returning
	if payloads != nil {
		err = c.checkPayloadsSize(payloads.Payloads)
		if err != nil {
			return nil, err
		}
	}
	return payloads, nil
}

func (c *payloadProcessingDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	innerConverter := c.DataConverter
	if contextAwareInnerConverter, ok := c.DataConverter.(ContextAware); ok {
		innerConverter = contextAwareInnerConverter.WithWorkflowContext(ctx)
	}

	newConverter := &payloadProcessingDataConverter{
		DataConverter:      innerConverter,
		logger:             GetLogger(ctx),
		payloadSizeWarning: c.payloadSizeWarning,
	}
	newConverter.errorLimits.Store(c.errorLimits.Load())
	return newConverter
}

func (c *payloadProcessingDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	logger := c.logger
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

	innerConverter := c.DataConverter
	if contextAwareInnerConverter, ok := c.DataConverter.(ContextAware); ok {
		innerConverter = contextAwareInnerConverter.WithContext(ctx)
	}

	newConverter := &payloadProcessingDataConverter{
		DataConverter:      innerConverter,
		logger:             logger,
		payloadSizeWarning: c.payloadSizeWarning,
	}
	newConverter.errorLimits.Store(c.errorLimits.Load())
	return newConverter
}

func (c *payloadProcessingDataConverter) checkPayloadsSize(payloads []*commonpb.Payload) error {
	var totalSize int64
	for _, payload := range payloads {
		if payload != nil {
			totalSize += int64(payload.Size())
		}
	}
	errorLimits := c.errorLimits.Load()
	if errorLimits != nil && errorLimits.PayloadSizeError > 0 && totalSize > errorLimits.PayloadSizeError {
		return payloadSizeError{
			message: "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.",
			size:    totalSize,
			limit:   errorLimits.PayloadSizeError,
		}
	}
	if c.payloadSizeWarning > 0 && totalSize > int64(c.payloadSizeWarning) && c.logger != nil {
		c.logger.Warn(
			"[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.",
			tagPayloadSize, totalSize,
			tagPayloadSizeLimit, int64(c.payloadSizeWarning),
		)
	}
	return nil
}
