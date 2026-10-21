package payloadlimits

import (
	"context"
	"errors"
	"sync/atomic"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/log"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// payloadSizeError is how a worker sees a request rejected for exceeding the payload error limits.
// Its type name becomes the type of the task failure that replaces the request, so it is kept stable.
type payloadSizeError struct {
	violation *Violation
}

func (e payloadSizeError) Error() string {
	return e.violation.Error()
}

// NewError returns the error a request rejected for violation v fails with.
func NewError(v *Violation) error {
	return payloadSizeError{violation: v}
}

// IsError reports whether err is a request rejected for exceeding the payload error limits.
func IsError(err error) bool {
	return errors.As(err, new(payloadSizeError))
}

// LogTags returns the log tags describing err when it is a request rejected for exceeding the
// payload error limits.
func LogTags(err error) []any {
	var e payloadSizeError
	if !errors.As(err, &e) {
		return nil
	}
	return logTags(e.violation)
}

// ValidateErrors checks req against only the error thresholds in limits and returns the error a
// request rejected through NewConn fails with. It logs nothing.
func ValidateErrors(req proto.Message, limits Limits) error {
	var violation *Violation
	if err := Validate(req, Limits{BlobError: limits.BlobError, MemoError: limits.MemoError}, nil); errors.As(err, &violation) {
		return payloadSizeError{violation: violation}
	}
	return nil
}

// conn validates every request sent through it against the payload limits before passing it on,
// so a rejected request never reaches the gRPC interceptors or the server. Each client and worker
// sends through its own, since the warning thresholds and logger are per client and only workers
// enforce the namespace's error limits.
type conn struct {
	grpc.ClientConnInterface
	warningLimits Limits
	// errorLimits is nil for clients, which only warn. A worker stores the namespace's limits once it
	// has described the namespace.
	errorLimits *atomic.Pointer[Limits]
	logger      log.Logger
}

var _ grpc.ClientConnInterface = (*conn)(nil)

// NewConn wraps cc so that requests sent through it are validated: violations of the warning
// thresholds in warningLimits are logged, and violations of the error thresholds in errorLimits
// fail the request without sending it.
func NewConn(
	cc grpc.ClientConnInterface,
	warningLimits Limits,
	errorLimits *atomic.Pointer[Limits],
	logger log.Logger,
) grpc.ClientConnInterface {
	return &conn{
		ClientConnInterface: cc,
		warningLimits:       warningLimits,
		errorLimits:         errorLimits,
		logger:              logger,
	}
}

func (c *conn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	if msg, ok := args.(proto.Message); ok {
		limits := Limits{BlobWarn: c.warningLimits.BlobWarn, MemoWarn: c.warningLimits.MemoWarn}
		if c.errorLimits != nil {
			if errorLimits := c.errorLimits.Load(); errorLimits != nil {
				limits.BlobError = errorLimits.BlobError
				limits.MemoError = errorLimits.MemoError
			}
		}
		var violation *Violation
		if err := Validate(msg, limits, c.logger); errors.As(err, &violation) {
			return payloadSizeError{violation: violation}
		}
	}
	return c.ClientConnInterface.Invoke(ctx, method, args, reply, opts...)
}

// FailOversizedQueryResults replaces each query answer over limit with a failed query result, so an
// oversized answer is not sent.
func FailOversizedQueryResults(msg proto.Message, limit int64) {
	if limit <= 0 {
		return
	}
	message := (&Violation{Class: LimitClassBlob, Severity: SeverityError}).Error()
	switch m := msg.(type) {
	case *workflowservice.RespondQueryTaskCompletedRequest:
		if int64(proto.Size(m.GetQueryResult())) > limit {
			m.QueryResult = nil
			m.ErrorMessage = message
			m.CompletedType = enumspb.QUERY_RESULT_TYPE_FAILED
		}
	case *workflowservice.RespondWorkflowTaskCompletedRequest:
		for _, result := range m.GetQueryResults() {
			if int64(proto.Size(result.GetAnswer())) > limit {
				result.Answer = nil
				result.ErrorMessage = message
				result.ResultType = enumspb.QUERY_RESULT_TYPE_FAILED
			}
		}
	}
}
