package internal

import (
	"context"
	"errors"
	"sync/atomic"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
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

type skipPayloadLimitsKey struct{}

func WithSkipPayloadLimits(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipPayloadLimitsKey{}, true)
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

var _ PayloadVisitorWithContextHook = (*payloadLimitsVisitorImpl)(nil)

func (v *payloadLimitsVisitorImpl) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}

func (v *payloadLimitsVisitorImpl) ContextHook(ctx context.Context, msg proto.Message) (context.Context, error) {
	if ctx != nil && ctx.Value(skipPayloadLimitsKey{}) == true {
		return ctx, nil
	}

	payloads := msg.(*commonpb.Payloads)
	if payloads != nil {
		size := int64(payloads.Size())
		errorLimits := v.errorLimits.Load()
		if errorLimits != nil && errorLimits.payloadSize > 0 && size > errorLimits.payloadSize {
			return nil, payloadSizeError{
				message: "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.",
				size:    size,
				limit:   errorLimits.payloadSize,
			}
		}
		if v.warningLimits.payloadSize > 0 && size > v.warningLimits.payloadSize && v.logger != nil {
			v.logger.Warn(
				"[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.",
				tagPayloadSize, size,
				tagPayloadSizeLimit, v.warningLimits.payloadSize,
			)
		}
	}

	return ctx, nil
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
