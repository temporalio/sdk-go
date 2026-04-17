package internal

import (
	"context"
	"errors"
	"sync/atomic"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/proxy"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/log"
	"google.golang.org/protobuf/proto"
)

// PayloadLimitOptions for when payload sizes exceed limits.
//
// NOTE: Experimental
//
// Exposed as: [go.temporal.io/sdk/client.PayloadLimitOptions]
type PayloadLimitOptions struct {
	// The limit (in bytes) at which a payload size warning is logged.
	// If unspecified or zero, defaults to 512 KiB.
	PayloadSizeWarning int
}

type skipErrorLimitKey struct{}
type skipWarningLimitKey struct{}

func withSkipErrorLimit(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipErrorLimitKey{}, true)
}

func withSkipWarningLimit(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipWarningLimitKey{}, true)
}

type payloadSizeError struct {
	message string
	size    int64
	limit   int64
}

func (e payloadSizeError) Error() string {
	return e.message
}

type payloadLimits struct {
	payloadSize int64
}

func payloadLimitOptionsToLimits(options PayloadLimitOptions) (payloadLimits, error) {
	payloadSizeWarning := int64(options.PayloadSizeWarning)
	if payloadSizeWarning < 0 {
		return payloadLimits{}, errors.New("PayloadSizeWarning must be greater than or equal to zero")
	}
	if payloadSizeWarning == 0 {
		payloadSizeWarning = 512 * 1024
	}
	return payloadLimits{
		payloadSize: payloadSizeWarning,
	}, nil
}

type payloadLimitsVisitorImpl struct {
	errorLimits   atomic.Pointer[payloadLimits]
	warningLimits payloadLimits
	logger        log.Logger
}

var _ PayloadVisitor = (*payloadLimitsVisitorImpl)(nil)
var _ PayloadVisitorWithContextHook = (*payloadLimitsVisitorImpl)(nil)

func (v *payloadLimitsVisitorImpl) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	skipErrorLimit := ctx.Context != nil && ctx.Context.Value(skipErrorLimitKey{}) == true
	skipWarningLimit := ctx.Context != nil && ctx.Context.Value(skipWarningLimitKey{}) == true

	size := int64(0)
	if ctx.SinglePayloadRequired {
		if _, ok := ctx.Parent.(*commandpb.ScheduleNexusOperationCommandAttributes); !ok {
			return payloads, nil
		}
		if len(payloads) > 0 {
			size = int64(payloads[0].Size())
		}
	} else {
		// Rewrap into Payloads to get the measured size that the server would also observe.
		size = int64((&commonpb.Payloads{Payloads: payloads}).Size())
	}

	err := v.checkPayloadSize(size, !skipErrorLimit, !skipWarningLimit)
	if err != nil {
		return nil, err
	}
	return payloads, nil
}

// ContextHook is used here to specialize the limit check logic based on how server has one-off decisions
// for each proto and field that contains payloads.
func (v *payloadLimitsVisitorImpl) ContextHook(ctx context.Context, msg proto.Message) (context.Context, error) {
	switch msg := msg.(type) {
	// RecordMarkerCommandAttributes.Details is a map[string]Payloads
	// Server has specialized size checking for map[string]Payloads for this field.
	case *commandpb.RecordMarkerCommandAttributes:
		err := v.checkPayloadSize(v.getPayloadsMapSize(msg.Details), true, true)
		if err != nil {
			return nil, err
		}
		ctx = withSkipErrorLimit(withSkipWarningLimit(ctx))
	// UpsertWorkflowSearchAttributesCommandAttributes.SearchAttributes is a map[string]Payload
	// Server has specialized size checking for map[string]Payload (that are not Memo fields).
	case *commandpb.UpsertWorkflowSearchAttributesCommandAttributes:
		err := v.checkPayloadSize(v.getPayloadMapSize(msg.GetSearchAttributes().GetIndexedFields()), true, true)
		if err != nil {
			return nil, err
		}
		ctx = withSkipErrorLimit(withSkipWarningLimit(ctx))
	// ModifyWorkflowPropertiesCommandAttributes.Properties is a map[string]Payload
	// Server has specialized size checking for map[string]Payload (that are not Memo fields).
	case *commandpb.ModifyWorkflowPropertiesCommandAttributes:
		err := v.checkPayloadSize(v.getPayloadMapSize(msg.GetUpsertedMemo().GetFields()), true, true)
		if err != nil {
			return nil, err
		}
		ctx = withSkipErrorLimit(withSkipWarningLimit(ctx))
	case *querypb.WorkflowQueryResult:
		err := v.checkPayloadSize(int64(msg.GetAnswer().Size()), true, true)
		// Server translates too large results into failed query results
		if err != nil {
			msg.Answer = nil
			msg.ErrorMessage = err.Error()
			msg.ResultType = enumspb.QUERY_RESULT_TYPE_FAILED
		}
		ctx = withSkipErrorLimit(withSkipWarningLimit(ctx))
	case *workflowservice.RespondQueryTaskCompletedRequest:
		err := v.checkPayloadSize(int64(msg.GetQueryResult().Size()), true, true)
		// Server translates too large results into failed query results
		if err != nil {
			msg.ErrorMessage = err.Error()
			msg.QueryResult = nil
			msg.CompletedType = enumspb.QUERY_RESULT_TYPE_FAILED
		}
		ctx = withSkipErrorLimit(withSkipWarningLimit(ctx))
	// Failures are passed through to server which will append failure details instead of failing the workflow.
	// Skip error checking these to allow the server to receive the failures.
	case *workflowservice.RespondActivityTaskFailedRequest:
		ctx = withSkipErrorLimit(ctx)
	case *workflowservice.RespondActivityTaskFailedByIdRequest:
		ctx = withSkipErrorLimit(ctx)
	case *workflowservice.RespondWorkflowTaskFailedRequest:
		ctx = withSkipErrorLimit(ctx)
	}
	return ctx, nil
}

func (v *payloadLimitsVisitorImpl) checkPayloadSize(size int64, checkErrorLimits bool, checkWarningLimits bool) error {
	if checkErrorLimits {
		errorLimits := v.errorLimits.Load()
		if errorLimits != nil && errorLimits.payloadSize > 0 && size > errorLimits.payloadSize {
			return payloadSizeError{
				message: "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.",
				size:    size,
				limit:   errorLimits.payloadSize,
			}
		}
	}
	if checkWarningLimits && v.warningLimits.payloadSize > 0 && size > v.warningLimits.payloadSize && v.logger != nil {
		v.logger.Warn(
			"[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.",
			tagPayloadSize, size,
			tagPayloadSizeLimit, v.warningLimits.payloadSize,
		)
	}
	return nil
}

func (v *payloadLimitsVisitorImpl) getPayloadMapSize(fields map[string]*commonpb.Payload) int64 {
	result := int64(0)

	for k, v := range fields {
		result += int64(len(k))
		// Intentionally measure data size, not payload size, to match server behavior.
		result += int64(len(v.GetData()))
	}
	return result
}

func (v *payloadLimitsVisitorImpl) getPayloadsMapSize(data map[string]*commonpb.Payloads) int64 {
	size := int64(0)
	for key, payloads := range data {
		size += int64(len(key))
		size += int64(payloads.Size())
	}
	return size
}

func (v *payloadLimitsVisitorImpl) setErrorLimits(errorLimits *payloadLimits) {
	v.errorLimits.Store(errorLimits)
}

func newPayloadLimitsVisitor(warningLimits payloadLimits, logger log.Logger) (PayloadVisitor, func(*payloadLimits)) {
	visitor := &payloadLimitsVisitorImpl{
		warningLimits: warningLimits,
		logger:        logger,
	}
	return visitor, visitor.setErrorLimits
}
